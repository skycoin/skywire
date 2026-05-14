# How to create a GitHub release

1. Make sure that `git` and [goreleaser](https://goreleaser.com/install) are installed.
2. Checkout to a commit you would like to create a release against.
3. Run `go mod vendor` and `go mod tidy`.
4. Make sure that `git status` is in clean state. Commit all vendor changes and source code changes.
5. Uncomment `draft: true` in `.goreleaser.yml` if this is a test release.
6. Create a `git` tag with desired release version and release name:
   `git tag -a 0.1.0 -m "First release"`, where `0.1.0` is release
   version and `First release` is release name.
7. Push the created tag to the repository: `git push origin 0.1.0`.
8. [Check the created GitHub release.](https://github.com/skycoin/skywire/releases/)
