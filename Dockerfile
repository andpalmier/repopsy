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
    && adduser -D -u 1000 repopsy \
    # A bind-mounted repository belongs to a different uid than the container
    # user, so git refuses it as "dubious ownership" and the documented
    # invocation fails for every repository. This image exists only to read
    # whatever repository is mounted into it, and it never writes to that
    # repository, so the ownership check protects nothing here.
    && git config --system --add safe.directory '*' \
    # /data is the working directory, so it is where snapshots land when no -o
    # is given. It must be writable by the container user or the simplest
    # invocation fails, and it is meant to be bind-mounted so the output
    # survives the container.
    && mkdir -p /data \
    && chown repopsy:repopsy /data

ARG TARGETPLATFORM
COPY $TARGETPLATFORM/repopsy /usr/local/bin/repopsy

WORKDIR /data
USER repopsy
ENTRYPOINT ["repopsy"]
