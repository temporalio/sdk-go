# Developing Temporal Go SDK

This doc is intended for contributors to `temporal-go-sdk` (hopefully that's you!)

**Note:** All contributors also need to fill out the [Temporal Contributor License Agreement](https://gist.github.com/samarabbas/7dcd41eb1d847e12263cc961ccfdb197) before we can merge in any of your changes

## Development Environment

* Go. Install on OS X with `brew install go`. The minimum required Go version is 1.10.

## Checking out the code

Make sure the repository is cloned to the preferred location:
(Note: the path is `go.temporal.io/temporal/` instead of github repo)

```bash
go get go.temporal.io/temporal/...
```

## Licence headers

This project is Open Source Software, and requires a header at the beginning of
all source files. To verify that all files contain the header execute:

```lang=bash
make copyright
```

## Commit Messages

Overcommit adds some requirements to your commit messages. At Temporal, we follow the
[Chris Beams](http://chris.beams.io/posts/git-commit/) guide to writing git
commit messages. Read it, follow it, learn it, love it.

## Testing

Run all the tests with coverage and race detector enabled:

```bash
make test
```
