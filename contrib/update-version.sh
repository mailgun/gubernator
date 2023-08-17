#!/bin/sh
# Update version numbers stored in repo.

version=$(echo $1 | sed 's/^v//')

usage () {
  echo "Usage: $0 <version>"
  echo
  echo "    version   Semver version string, such as: 2.0.1"
}

if [ -z "$version" ]; then
  usage >&2
  exit 1
fi

echo "Setting version: $version"

echo "Updating version file..."
echo v$version > version

echo "Updating Helm chart..."
sed -i '' 's/^version:.*$/version: '$version'/' contrib/charts/gubernator/Chart.yaml
sed -i '' 's/^appVersion:.*$/appVersion: '$version'/' contrib/charts/gubernator/Chart.yaml
