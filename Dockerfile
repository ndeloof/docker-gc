FROM scratch

COPY /docker-gc /docker-gc

ENTRYPOINT ["/docker-gc"]
