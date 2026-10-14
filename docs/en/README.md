# Documentation

Language: **English** · [简体中文](../zh-CN/README.md)

---

## Start here

| If you want to… | Read |
|---|---|
| **Build it yourself** | [dev/upload-and-build.md](dev/upload-and-build.md) |
| **Know what has actually been verified** | [dev/verification.md](dev/verification.md) |
| **Understand how the pieces fit** | [dev/architecture.md](dev/architecture.md) |
| **Use it** | [use/README.md](use/README.md) |

---

## The one thing to know first

**None of the code has ever been compiled.**

The development machine has no Go, C++, JDK or Android SDK toolchain.
Every compile and every unit test runs in GitHub Actions, and the result
there is the only authority.

The [verification page](dev/verification.md) lists exactly which checks
have been done locally, which can only run in CI, and which have **never**
been done at all. Unrun checks are reported as `not_run` and are never
written as "passed".

---

## Project documents

| Document | Contents |
|---|---|
| [design/DESIGN.en.md](../../design/DESIGN.en.md) | The design baseline: requirements and constraints |
| [design/adr/](../../design/adr/) | Architecture decision records |
| [design/plan/STATUS.md](../../design/plan/STATUS.md) | Stage and completion status |
| [design/CONFIGURATION.md](../../design/CONFIGURATION.md) | The shipped configuration file |
| [design/architecture/i18n-convention.md](../../design/architecture/i18n-convention.md) | Localisation conventions |
| [design/plan/environment-constraints.md](../../design/plan/environment-constraints.md) | Why this machine cannot compile anything |
| [deploy/packaging/README.md](../../src/versions/0.1.0/deploy/packaging/README.md) | Packaging contract |
| [deploy/docker/README.md](../../src/versions/0.1.0/deploy/docker/README.md) | Docker contract (deliberately unimplemented) |

---

## What this is

A dual-core (Xray / sing-box) proxy client for Xboard panels, with desktop
(Windows, Linux) and Android front ends.

It is a **client**: it provides no proxy service of its own. You need your
own panel account or subscription link.
