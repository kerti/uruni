# Security policy

## Reporting a vulnerability

Please report security issues **privately** via GitHub's **"Report a vulnerability"** button under this repository's **Security** tab (Private Vulnerability Reporting). Do not open a public issue for a suspected vulnerability.

Include, where possible: what you found, how to reproduce it, and the potential impact.

## What to expect

Uruni is maintained by one person, part-time. These are commitments I can keep, not a service-level agreement:

| Step | Within |
| --- | --- |
| Acknowledgement of your report | 7 days |
| Initial assessment (is it a vulnerability, how severe) | 14 days |
| Fix for a **critical or high** severity issue | 30 days |
| Fix for a **medium or low** severity issue | the next planned release |

If a deadline is at risk I will say so in the report thread rather than go quiet. Credit in the advisory is yours if you want it.

## How fixes reach operators

Each fix is announced through a **GitHub Security Advisory** on this repository and in the **release notes** of the release that carries it. There is no mailing list and no central service that can push an update to you: operators upgrade their own instance (see `SELF-HOSTING.md`).

To hear about it, **watch this repository**: *Watch -> Custom -> Releases and Security alerts*.

## Scope

Uruni is **self-hosted**: each community runs its own instance, and the project operates no central service holding anyone's data. Reports about the Uruni code - the API, the public report page, authentication, backups, the container image - are in scope. Issues in a *particular operator's* deployment (their server, their TLS, their volumes) are the operator's responsibility, though reports that reveal a weakness in the defaults we ship are welcome.

## Supported versions

During the `0.x` pre-release ramp, only the latest tagged release is supported. The schema is not guaranteed stable between alphas.
