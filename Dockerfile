# The binary is not compiled here. It is copied in, so the image ships the exact
# artifact that the release archives contain rather than a second build of the
# same commit — which matters for a tool whose output is meant to be evidence.
#
# GoReleaser lays the build context out as <os>/<arch>/repopsy and buildx sets
# TARGETPLATFORM to match. CI reproduces that layout before building, so the
# image it tests is the image that ships.
FROM alpine:3.24

# git is the only runtime dependency.
RUN apk add --no-cache git \
    && adduser -D -u 1000 repopsy

ARG TARGETPLATFORM
COPY $TARGETPLATFORM/repopsy /usr/local/bin/repopsy

WORKDIR /data
USER repopsy
ENTRYPOINT ["repopsy"]
