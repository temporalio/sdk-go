# Developing Temporal Go SDK

This doc is intended for contributors to Go SDK (hopefully that's you!)

**Note:** All contributors also need to fill out the [Temporal Contributor License Agreement](https://gist.github.com/samarabbas/7dcd41eb1d847e12263cc961ccfdb197) before we can merge in any of your changes

## Development Environment

* [Go Lang](https://golang.org/) (minimum version required is 1.14):
  - Install on OS X with `brew install go`.
  - Install on Ubuntu with `sudo apt install golang`.

## Checking out the code

Temporal GO SDK uses go modules, there is no dependency on `$GOPATH` variable. Clone the repo into the preffered location:
```bash
git clone https://github.com/temporalio/sdk-go.git
```

## Licence headers

This project is Open Source Software, and requires a header at the beginning of
all source files. To verify that all files contain the header execute:

```bash
make copyright
```

## Commit Messages And Titles of Pull Requests

Overcommit adds some requirements to your commit messages. At Temporal, we follow the
[Chris Beams](http://chris.beams.io/posts/git-commit/) guide to writing git
commit messages. Read it, follow it, learn it, love it.

All commit messages are from the titles of your pull requests. So make sure follow the rules when titling them. 
Please don't use very generic titles like "bug fixes". 

All PR titles should start with Upper case.

## Testing

Run all static analysis tools:
```bash
make check
```

Run all the tests (including integration tests, requiring you are running a server locally - see
[here](https://github.com/temporalio/temporal/blob/master/CONTRIBUTING.md) with coverage and race detector enabled:

```bash
make test
```

To run just the unit tests:
```bash
make unit-test
```
