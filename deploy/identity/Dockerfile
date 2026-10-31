# syntax=docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e
ARG KEYCLOAK_IMAGE=quay.io/keycloak/keycloak:26.8.1@sha256:a78d6b79572168cb25886b80a8b5b8d4ed4390743e3eecb810538683e209dd6b
ARG NODE_IMAGE=node:24.22.2-alpine3.24@sha256:8a32c8cf649a49915a66e81e7a609223c708d0adabf288ee3f4077d765542f85
ARG UBI_IMAGE=registry.access.redhat.com/ubi9/ubi:9.8@sha256:5f08e80aa970ae77712e75e5e857ac0b396abc900823c4d041c1150a9538a13f
FROM ${KEYCLOAK_IMAGE} AS upstream
FROM ${UBI_IMAGE} AS security-runtime
# Follow Keycloak's installroot pattern: no package manager is added to the runtime image.
COPY --from=upstream / /mnt/rootfs/
RUN dnf -y --installroot=/mnt/rootfs --releasever=9 --setopt=install_weak_deps=false --nodocs \
      update java-21-openjdk-headless ca-certificates \
    && dnf --installroot=/mnt/rootfs clean all \
    && rm -rf /mnt/rootfs/var/cache/dnf /mnt/rootfs/var/log/dnf*

FROM --platform=$BUILDPLATFORM ${NODE_IMAGE} AS theme
WORKDIR /source
COPY package.json package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --ignore-scripts --no-audit --no-fund
COPY web/ web/
COPY deploy/identity/theme.css deploy/identity/theme.css
COPY deploy/identity/themes deploy/identity/themes
COPY scripts/build-identity-theme.mjs scripts/build-identity-theme.mjs
RUN node scripts/build-identity-theme.mjs

FROM scratch AS patched
COPY --from=security-runtime /mnt/rootfs/ /

FROM patched AS build
USER 0
# Keep the upstream classpath filename; the checked JAR's Maven metadata/version is 33.7.2-jre.
RUN test -f /opt/keycloak/lib/lib/main/com.google.guava.guava-33.6.0-jre.jar
ADD --checksum=sha256:b530942257fb935f8b2cfaa5f8eb5bd59c555fd8e8d01b8ce98912e077ea606c --chown=1000:0 --chmod=0644 https://repo.maven.apache.org/maven2/com/google/guava/guava/33.7.2-jre/guava-33.7.2-jre.jar /opt/keycloak/lib/lib/main/com.google.guava.guava-33.6.0-jre.jar
# Stable OpenTelemetry API/context patch for bounded W3C tracestate parsing.
RUN test -f /opt/keycloak/lib/lib/main/io.opentelemetry.opentelemetry-api-1.62.0.jar \
    && test -f /opt/keycloak/lib/lib/main/io.opentelemetry.opentelemetry-context-1.62.0.jar
ADD --checksum=sha256:538d1dff2f9a37aecbc0ef98fd17273a3affb058e734e6763a3a07a38a5fdc59 --chown=1000:0 --chmod=0644 https://repo.maven.apache.org/maven2/io/opentelemetry/opentelemetry-api/1.67.0/opentelemetry-api-1.67.0.jar /opt/keycloak/lib/lib/main/io.opentelemetry.opentelemetry-api-1.62.0.jar
ADD --checksum=sha256:40fb813fb5e27ec2691daa29c81b572b5b1541ef9b41e62490c3ee3e6407c55e --chown=1000:0 --chmod=0644 https://repo.maven.apache.org/maven2/io/opentelemetry/opentelemetry-context/1.67.0/opentelemetry-context-1.67.0.jar /opt/keycloak/lib/lib/main/io.opentelemetry.opentelemetry-context-1.62.0.jar
USER 1000:0
ENV JAVA_HOME=/usr/lib/jvm/jre-21-openjdk
ENV LANG=en_US.UTF-8 KC_RUN_IN_CONTAINER=true
WORKDIR /opt/keycloak
ARG IDENTITY_HTTP_PREFIX=/auth
ENV KC_DB=postgres KC_HEALTH_ENABLED=true KC_METRICS_ENABLED=true KC_HTTP_RELATIVE_PATH=${IDENTITY_HTTP_PREFIX} KC_HTTP_MANAGEMENT_RELATIVE_PATH=/
RUN /opt/keycloak/bin/kc.sh build

FROM patched
ARG IDENTITY_HTTP_PREFIX=/auth
ENV KC_HTTP_RELATIVE_PATH=${IDENTITY_HTTP_PREFIX} KC_HTTP_MANAGEMENT_RELATIVE_PATH=/
ENV JAVA_HOME=/usr/lib/jvm/jre-21-openjdk
ENV LANG=en_US.UTF-8 KC_RUN_IN_CONTAINER=true
WORKDIR /opt/keycloak
COPY --from=build /opt/keycloak/ /opt/keycloak/
COPY --from=theme /source/build/identity-theme/gitmora /opt/keycloak/themes/gitmora
COPY deploy/identity/themes/KEYCLOAK-LICENSE.txt /opt/keycloak/themes/KEYCLOAK-LICENSE.txt
USER 1000:0
ENTRYPOINT ["/opt/keycloak/bin/kc.sh"]
CMD ["--config-file=/run/secrets/keycloak.conf", "start", "--optimized", "--import-realm"]
