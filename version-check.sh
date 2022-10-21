#!/bin/sh

# Use git tag as reference version string.
# Strip leading 'v'.
VERSION=$(git describe --tags $(git rev-list --tags --max-count=1) | sed -e 's/^v//')
if [ -z "$VERSION" ]; then
  echo "Unable to determine version from git tags." >&1
  exit 1
fi

echo "Version tag: $VERSION"
RETCODE=0

# Check version file.
if [ ! "v$VERSION" = "$(cat version)" ]; then
  echo "version file mismatch: v$VERSION <=> $(cat version)" >&1
  RETCODE=1
else
  echo 'version file OK'
fi

# Check Helm chart.
HELM_VERSION=$(yq .version charts/gubernator/Chart.yaml)
if [ "$VERSION" != "$HELM_VERSION" ]; then
  echo "Helm chart version mismatch: $VERSION <=> $HELM_VERSION" >&1
  RETCODE=1
else
  echo 'Helm chart version OK'
fi

HELM_APPVERSION=$(yq .appVersion charts/gubernator/Chart.yaml)
if [ "$VERSION" != "$HELM_APPVERSION" ]; then
  echo "Helm chart appVersion mismatch: $VERSION <=> $HELM_APPVERSION" >&1
  RETCODE=1
else
  echo 'Helm chart appVersion OK'
fi

exit $RETCODE
