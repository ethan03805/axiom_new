# Default Runtime Image

This directory is the canonical build context for Axiom's shipped default
worker image.

Build the image from a source checkout or release bundle root with:

```bash
docker build -t axiom-meeseeks-multi:latest -f docker/meeseeks-multi.Dockerfile docker
```

Current source-controlled assets in this checkout cover the default
multi-language image only. Single-language image tags such as
`axiom-meeseeks-go:latest`, `axiom-meeseeks-node:latest`, and
`axiom-meeseeks-python:latest` remain optional future or custom variants.
