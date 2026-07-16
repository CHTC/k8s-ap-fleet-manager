# Third-Party Notices

This project vendors the following third-party content.

## Traefik

- **File:** `config/crds/testdata/ingressroutetcp-crd.yaml`
- **What:** A copy of the `IngressRouteTCP` CustomResourceDefinition, extracted from Traefik's
  published CRD bundle so envtest can install it locally (envtest requires a file on disk, not
  a URL). Pinned to the same `v3.7` tag referenced by `config/crds/kustomization.yaml`.
- **Source:** https://raw.githubusercontent.com/traefik/traefik/v3.7/docs/content/reference/dynamic-configuration/kubernetes-crd-definition-v1.yml
- **License:** MIT (Copyright (c) 2016-2020 Containous SAS; 2020-2025 Traefik Labs). Full license
  text is included as a header comment in the vendored file itself.

`internal/traefik/v1alpha1/` contains independently-written Go type definitions covering the
subset of the `IngressRouteTCP` schema this project sets. They were authored by reading the CRD's
field names/shape (as required for wire-format interoperability), not copied from Traefik's own
Go source.
