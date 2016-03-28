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
	"github.com/fsouza/go-dockerclient"
	log "github.com/Sirupsen/logrus"
	"time"
	"flag"
)

var client *docker.Client
var maxAge = flag.Duration("maxAge", 72 * time.Hour, "max duration for an unused image")
var debug = flag.Bool("debug", false, "Enable debug output")

func init() {
	c, err := docker.NewClientFromEnv()
	if err != nil {
		log.Fatal("Failed to setup docker client " + err.Error())
	}
	client = c
}

func main() {

	flag.Parse()

	if *debug {
		log.SetLevel(log.DebugLevel)
	}

	log.Infof("Will purge all images unused since last %v", maxAge)

	c := make(chan *docker.APIEvents)

	lastuse := map[string]time.Time{}
	ticker := time.NewTicker(time.Second * 10)

	client.AddEventListener(c)
	for {
		select {
		case e := <-c:
			if e.Status == "destroy" {
				log.Infof("Container using %s has been destroyed", e.From)

				// resolve tag into image ID if required
				image, err := client.InspectImage(e.From)
				if err != nil {
					log.Fatal(err)
				}
				lastuse[image.ID] = time.Now()
			}
		case <-ticker.C:
			collect(lastuse)
		}

	}
}


func collect(lastUse map[string]time.Time) {
	dangling, err := client.ListImages(docker.ListImagesOptions{Filter: "dangling=true" })
	if err != nil {
		log.Fatal(err)
	}
	for _, image := range dangling {
		log.Infof("Remove dangling image %v\n", image.ID)
		client.RemoveImage(image.ID)
	}

	inUse := map[string]bool{}
	containers, err := client.ListContainers(docker.ListContainersOptions{All:true})
	if err != nil {
		log.Fatal(err)
	}
	for _, container := range containers {
		inUse[container.Image] = true
	}

	max := time.Now().Add(time.Duration(-1 * maxAge.Nanoseconds()))
	log.Debugf("Purging all images unused since %v", max.Truncate(time.Second))
	images, err := client.ListImages(docker.ListImagesOptions{})
	if err != nil {
		log.Fatal(err)
	}
	for _, image := range images {
		id := image.ID
		if use, ok := lastUse[id]; ok && use.Before(max) && !inUse[id] {
			log.Infof("Image %s hasn't been used since %v", id, use)
			client.RemoveImage(id)
		}
	}

}
