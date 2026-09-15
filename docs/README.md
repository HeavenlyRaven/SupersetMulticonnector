# A guided tour of this project

This folder explains **every file in this repository**, and the ideas behind
them, assuming you know basic programming but are still learning Go,
Superset, SQL databases, and Docker.

It is written to be read **in order**, front to back. Each chapter builds on
the one before it. You do not need to read the code first — the guide walks
you into it.

## Reading order

> Just want to **run** it rather than study it?
> [INSTALL.md](INSTALL.md) is the end-user guide: prerequisites, the Windows
> installer, first-time setup, and troubleshooting.

| # | Chapter | What you get out of it |
|---|---|---|
| 1 | [What this project actually does](01-what-this-project-is.md) | The problem being solved, and the shape of the solution. Read this first, twice if needed. |
| 2 | [Background concepts](02-background-concepts.md) | Docker, SQL engines, Superset, and the Go idioms used here. Skim now, return when a term confuses you. |
| 3 | [Repository map](03-repository-map.md) | Every file in one table, with a one-line job description. Your index. |
| 4 | [`fedctl`, the Go CLI](04-fedctl-cli.md) | File by file through `cmd/fedctl/` — the command-line tool that does all the real work. |
| 5 | [The internal Go packages](05-internal-packages.md) | File by file through `internal/` — validation, DDL generation, the ClickHouse client, health checks. |
| 6 | [Containers and configuration](06-containers-and-configuration.md) | The Dockerfiles, Compose files, ClickHouse XML, and Superset config. |
| 7 | [The Superset extension](07-the-superset-extension.md) | The Python shim and the React frontend that put a panel inside SQL Lab. |
| 8 | [The tests](08-tests.md) | Every test file, and what each one proves. |
| 9 | [End-to-end walkthroughs](09-end-to-end-walkthroughs.md) | Follow one click and one command all the way down the stack and back. |
| 10 | [Glossary](10-glossary.md) | Every piece of jargon in the repo, defined. |

## How to read this alongside the code

Open two panes in your editor: this guide on one side, the file being
discussed on the other. Every chapter links to real files with relative
paths, so `Ctrl+Click` on a link opens the file.

When you hit a term you do not know — *named collection*, *SSRF*, *TOCTOU*,
*idempotent* — check the [glossary](10-glossary.md) rather than guessing.
Most of them are simpler than they sound.

## A note on the code you are about to read

This codebase is unusually heavily commented. That is deliberate: many
comments record something that was **discovered by testing against the real
system**, not something obvious from documentation. When you see a comment
starting with "Live-verified:", it is describing a real failure that was hit
and fixed. Those comments are some of the most valuable material in the
repo — read them, they are half the education.
