# syntax=docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e
FROM postgres:18.6-alpine3.24@sha256:10a5c69a83035ab5239f5620d708520e8b28f5aa1f2b20143e6024532edadfb5 AS patched
# Keep the database version/data format while refreshing supported distro security packages.
# The entrypoint only needs user switching; su-exec avoids retaining an obsolete Go runtime in gosu.
RUN apk upgrade --no-cache && apk add --no-cache su-exec \
    && rm /usr/local/bin/gosu && cp /sbin/su-exec /usr/local/bin/gosu

# Copy the merged filesystem so the retired Go binary does not remain in inherited layers.
FROM scratch
COPY --from=patched / /
ENV PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    LANG=en_US.utf8 PG_MAJOR=18 PG_VERSION=18.6 \
    PGDATA=/var/lib/postgresql/18/docker
WORKDIR /
VOLUME /var/lib/postgresql
EXPOSE 5432
STOPSIGNAL SIGINT
ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["postgres"]
