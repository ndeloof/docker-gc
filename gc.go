/**
 * Copyright 2015 Nicolas De Loof
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     https://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */
package main

import (
	"context"
	"flag"
	"github.com/boltdb/bolt"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	log "github.com/sirupsen/logrus"
	"os"
	"path"
	"strings"
	"time"
)

var (
	cli            *client.Client
	db             *bolt.DB
	dbPath         = flag.String("db", "/var/db/docker-gc/state.db", "Location of the database file")
	debug          = flag.Bool("debug", false, "Enable debug output")
	maxAge         = flag.Duration("maxAge", 72*time.Hour, "max duration for an unused image")
	lastUse        = map[string]time.Time{}
	purgeFrequency = flag.Duration("purgeFrequency", 57*time.Second, "How often the image purge will be run")
)

const (
	BUCKET_IMAGE = "images"
)

func init() {
	c, err := client.NewEnvClient()
	if err != nil {
		log.Fatal("Failed to setup docker client " + err.Error())
	}
	cli = c
}

func removeImage(id string) {
	log.WithField("id", id).Info("Removing image")
	_, err := cli.ImageRemove(context.Background(), id, types.ImageRemoveOptions{})
	if err != nil {
		log.WithError(err).WithField("id", id).Error("Cannot remove image")
		return
	}
	if db != nil {
		db.Update(func(tx *bolt.Tx) error {
			log.WithField("id", id).Debug("Removing image from database")
			b := tx.Bucket([]byte(BUCKET_IMAGE))
			err := b.Delete([]byte(id))
			if err != nil {
				log.WithError(err).WithField("id", id).Warn("Error while removing from database")
				return err
			}
			return nil
		})
	}
	delete(lastUse, id)
}

func updateImageLastUsage(id string, usage time.Time) {
	if db != nil {
		db.Update(func(tx *bolt.Tx) error {
			log.WithFields(log.Fields{"id": id, "usage": usage}).
				Debug("Updating database")
			b := tx.Bucket([]byte(BUCKET_IMAGE))
			encoded, err := usage.GobEncode()
			if err != nil {
				log.WithError(err).WithFields(log.Fields{"id": id, "usage": usage}).
					Error("Cannot update image data")
				return err
			}
			err = b.Put([]byte(id), encoded)
			if err != nil {
				log.WithError(err).WithFields(log.Fields{"id": id, "usage": usage}).
					Error("Cannot update image data")
				return err
			}
			return nil
		})
		// TODO what to do if db cannot be updated?
	}

	lastUse[id] = usage
}

func loadImageDataFromDocker() {
	now := time.Now()
	log.Info("Setting last use from containers")
	containers, err := cli.ContainerList(context.Background(), types.ContainerListOptions{All: true})
	if err != nil {
		log.WithError(err).Warn("Cannot get list of containers, image last usage may be less accurate")
	} else {
		for _, container := range containers {
			log.WithField("Container", container.ID).Debug("Reading container")
			img, _, err := cli.ImageInspectWithRaw(context.Background(), container.Image)
			if err != nil {
				log.WithError(err).WithField("Container", container.ID).
					Warn("Cannot inspect image for container")
				continue
			}
			var usage time.Time
			if strings.HasPrefix(container.Status, "Exit") {
				log.WithField("Container", container.ID).Debug("Container exited, adjusting image last usage")
				details, err := cli.ContainerInspect(context.Background(), container.ID)
				if err != nil {
					log.WithField("Container", container.ID).
						WithError(err).Warn("Cannot inspect container, skipping image update")
					continue
				}
				usage, err = time.Parse(time.RFC3339, details.State.FinishedAt)
				if err != nil {
					log.WithError(err).WithField("Container", container.ID).
						Warn("Cannot parse FinishedAt for container")
					continue
				}

			} else {
				usage = now
			}
			if old, ok := lastUse[img.ID]; !ok || old.Before(usage) {
				updateImageLastUsage(img.ID, usage)
			}
		}
	}

	log.Info("Reading image data from Docker")
	images, err := cli.ImageList(context.Background(), types.ImageListOptions{})
	if err != nil {
		log.WithError(err).Warn("Cannot list images from Docker")
		return
	}
	for _, image := range images {
		log.WithField("ID", image.ID).Debug("Reading image")
		if old, exists := lastUse[image.ID]; exists {
			log.WithField("ID", image.ID).WithField("Usage", old).Debug("Not updating image")
		} else {
			log.WithField("ID", image.ID).WithField("Usage", now).Debug("Updating image")
			updateImageLastUsage(image.ID, now)
		}
	}
}

func initDatabase() error {
	dirname := path.Dir(*dbPath)

	err := os.MkdirAll(dirname, 0700)

	if err != nil {
		log.WithError(err).WithField("Dir", dirname).Error("Cannot create db directory")
		return err
	}

	db, err = bolt.Open(*dbPath, 0600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		log.WithError(err).WithField("Path", *dbPath).Error("Cannot open database")
		return err
	}

	db.Update(func(tx *bolt.Tx) error {
		log.WithField("Bucket", BUCKET_IMAGE).Debug("Using bucket")
		b, err := tx.CreateBucketIfNotExists([]byte(BUCKET_IMAGE))

		if err != nil {
			log.WithError(err).Error("Cannot get or create bucket", BUCKET_IMAGE, err.Error())
			return err
		}

		c := b.Cursor()

		for id, usage := c.First(); id != nil; id, usage = c.Next() {
			// TODO do not restore data for image not longer existing & remove them from DB
			var decoded time.Time
			err := decoded.GobDecode(usage)
			if err != nil {
				log.WithError(err).WithField("Image", id).Warn("Cannot decode last usage")
			}
			log.WithFields(log.Fields{
				"Image":    string(id),
				"Last use": decoded,
			}).Debug("Retrieved image data")
			lastUse[string(id)] = decoded
		}
		return nil
	})
	return nil
}

func prepare() {
	err := initDatabase()
	if err != nil {
		log.WithError(err).Warn("Cannot init database, persistence disabled")
		if db != nil {
			db.Close()
			db = nil
		}
	}
	loadImageDataFromDocker()
	log.Infof("Loaded %d images from Docker", len(lastUse))
}

func main() {
	flag.Parse()

	if *debug {
		log.SetLevel(log.DebugLevel)
	}

	prepare()

	log.WithField("MaxAge", maxAge).Info("Will purge all images unused")

	ticker := time.NewTicker(*purgeFrequency)

	for {
		select {
		case <-ticker.C:
			collect()
		}

	}
}

func collect() {
	filters := filters.NewArgs()
	filters.Add("dangling", "true")
	dangling, err := cli.ImageList(context.Background(), types.ImageListOptions{Filters: filters})
	if err != nil {
		// TODO isn't Fatal a be too much
		log.WithError(err).Fatal("Cannot get list of dangling images")
	}
	for _, image := range dangling {
		log.WithField("id", image.ID).Info("Remove dangling image")
		removeImage(image.ID)
	}

	inUse := map[string]bool{}
	containers, err := cli.ContainerList(context.Background(), types.ContainerListOptions{All: true})
	if err != nil {
		// TODO isn't Fatal a be too much
		log.WithError(err).Fatal("Cannot get list of containers")
	}
	for _, container := range containers {
		img, _, err := cli.ImageInspectWithRaw(context.Background(), container.Image)
		if err != nil {
			log.WithError(err).WithField("Container", container.ID).
				Warn("Cannot inspect image for container")
			continue
		}
		log.WithFields(log.Fields{"image": img.ID, "container": container.ID}).Debug("Image is used by container")
		inUse[img.ID] = true
	}

	max := time.Now().Add(time.Duration(-1 * maxAge.Nanoseconds()))
	log.WithField("Since", max.Truncate(time.Second)).Debug("Purging all unused image")
	images, err := cli.ImageList(context.Background(), types.ImageListOptions{})
	if err != nil {
		log.Fatal(err)
	}
	for _, image := range images {
		id := image.ID
		if use, ok := lastUse[id]; ok && use.Before(max) && !inUse[id] {
			log.WithFields(log.Fields{"id": id, "use": use}).Info("Purging unused image")
			removeImage(image.ID)
		}
	}

}
