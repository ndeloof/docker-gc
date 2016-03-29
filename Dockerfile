FROM scratch

COPY /docker-gc /docker-gc

VOLUME /var/db/docker-gc

ENTRYPOINT ["/docker-gc"]
