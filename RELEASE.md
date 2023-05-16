# Release Process
Repository maintainers may be responsible for publishing new releases after
functionality is merged.

This document explains the release process for when a new version must be
published.

At a high level:
* Merge PR to `master`.
* Then, update version files in `master`.
* Finally, publish GitHub Release.

## Update Version Files
Some files contain the current version in [semver](https://semver.org/) format,
such as "2.0.0-rc.34".  These files must be updated to the target version.

These files are:
* `charts/helm/Chart.yaml`
   * `version`
   * `appVersion`
* `version`

Use script `update-version.sh` to easily update the required files.

```
$ ./update-version.sh 2.0.0-rc.35
```

Commit and push directly to `master` branch.

## Publish New GitHub Release
Publish a GitHub release from github.com.

Create a new tag with "v" prefix version, such as "v2.0.0-rc.34".

Provide a meaningful description of what's changed.  For example:

* Tag: v2.0.0-rc.35
* Title: v2.0.0-rc.35
```markdown
## What's Changed
* Updated foobar by @Baliedge in #999.
```

Click "Publish Release".

Publishing will launch an `on-release` GitHub Action to do the following:
* Check version consistency.
* Build Docker image.
* Publish Docker image.
* Publish Helm chart.

More details on publishing GitHub releases:
https://docs.github.com/en/repositories/releasing-projects-on-github/managing-releases-in-a-repository.

## Version Consistency Check
The version consistency check is performed in both PR `on-pull-request` and
`on-release`.  This will ensure the latest tag version matches the version
found in the files described in [Update Version Files](#update-version-files).

If the check fails, the workflow will abort with an error.  The developer can
make the necessary changes indicated in the error message.

Developers may call script `./check-version.sh` locally to verify changes
before commit.
