#!/bin/sh

# Use git tag as reference version string.
# Strip leading 'v'.
VERSION=$(git describe --tags $(git rev-list --tags --max-count=1) | sed -e 's/^v//')
if [ -z "$VERSION" ]; then
  >1 echo "Unable to determine version from git tags."
  exit 1
fi

echo "Version: $VERSION"

# Check version file.
grep "^$VERSION$" version || (
  echo "version file mismatch: $VERSION <=> $(cat version)" >1
  exit 1
)
echo 'version file OK'

# Check Helm chart.
HELM_VERSION=$(yq .version charts/gubernator/Chart.yaml)
if [ "$VERSION" != "$HELM_VERSION" ]; then
  echo "Helm chart version mismatch: $VERSION <=> $HELM_VERSION" >1
  exit 1
fi
echo 'Helm chart version OK'

HELM_APPVERSION=$(yq .appVersion charts/gubernator/Chart.yaml)
if [ "$VERSION" != "$HELM_APPVERSION" ]; then
  echo "Helm chart appVersion mismatch: $VERSION <=> $HELM_APPVERSION" >1
  exit 1
fi
echo 'Helm chart appVersion OK'
