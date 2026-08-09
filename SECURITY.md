# Security policy

## Reporting a vulnerability

Please report security issues **privately** via GitHub's **"Report a vulnerability"** button under this repository's **Security** tab (Private Vulnerability Reporting). Do not open a public issue for a suspected vulnerability.

Include, where possible: what you found, how to reproduce it, and the potential impact. We'll acknowledge the report and keep you updated on the fix.

## Scope

Uruni is **self-hosted**: each community runs its own instance, and the project operates no central service holding anyone's data. Reports about the Uruni code — the API, the public report page, authentication, backups, the container image — are in scope. Issues in a *particular operator's* deployment (their server, their TLS, their volumes) are the operator's responsibility, though we welcome reports that reveal a weakness in the defaults we ship.

## Supported versions

During the `0.x` pre-release ramp, only the latest tagged release is supported. The schema is not guaranteed stable between alphas.
