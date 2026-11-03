# syntax=docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e
FROM postgres:18.6-alpine3.24@sha256:10a5c69a83035ab5239f5620d708520e8b28f5aa1f2b20143e6024532edadfb5 AS patched
# Keep the database version/data format while refreshing supported distro security packages.
# The entrypoint only needs user switching; su-exec avoids retaining an obsolete Go runtime in gosu.
RUN apk upgrade --no-cache && apk add --no-cache su-exec \
    && rm /usr/local/bin/gosu && cp /sbin/su-exec /usr/local/bin/gosu
RUN apk add --no-cache --virtual .vector-build build-base clang21 llvm21-dev curl \
    && curl -fsSL https://github.com/pgvector/pgvector/archive/refs/tags/v0.8.7.tar.gz -o /tmp/vector.tar.gz \
    && echo 'cac0b10c360f05b2d521200105ba3697e773d4cd3731f5a915a7e37ebe0bea85  /tmp/vector.tar.gz' | sha256sum -c - \
    && tar -xzf /tmp/vector.tar.gz -C /tmp \
    && make -C /tmp/pgvector-0.8.7 OPTFLAGS='' \
    && make -C /tmp/pgvector-0.8.7 install \
    && mkdir -p /usr/local/share/licenses/pgvector \
    && cp /tmp/pgvector-0.8.7/LICENSE /usr/local/share/licenses/pgvector/LICENSE \
    && rm -rf /tmp/pgvector-0.8.7 /tmp/vector.tar.gz \
    && apk del .vector-build

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
