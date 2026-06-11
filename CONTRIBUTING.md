# Contributing to kmorph

Thank you for your interest in contributing!

## Getting Started

1. Fork the repository and clone locally
2. Install prerequisites: Go 1.25+, kubebuilder 4.x, podman
3. Run `make generate manifests` to generate CRDs
4. Run `make test` to verify all tests pass

## Pull Requests

- One feature or fix per PR
- Add tests for new functionality
- Run `make generate manifests` before committing if you change API types
- Update `README.md` if you add user-facing features

## Reporting Issues

Use [GitHub Issues](https://github.com/n0rm4l-me/kmorph/issues).
