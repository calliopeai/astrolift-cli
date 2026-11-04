# Studio round trip

This page describes how a ticket in Astrolift Studio becomes a factory run on this repository. The path has four steps: the ticket is mirrored to a GitHub issue, the factory launcher starts the toolchain workflow, the stages leave signed receipts on the issue, and a draft pull request carries the change.

## 1. The ticket is mirrored to a GitHub issue

A ticket created in Astrolift Studio is mirrored to a GitHub issue in this repository. The issue is where the rest of the run shows up.

## 2. The factory launcher starts the toolchain workflow

The factory launcher starts the toolchain workflow for the mirrored issue.

## 3. The stages leave signed receipts on the issue

As the toolchain workflow moves through its stages, each stage leaves a signed receipt on the GitHub issue.

## 4. A draft pull request carries the change

The change arrives as a draft pull request on this repository.

## More detail

This repository holds no Studio or factory source. For details beyond these four steps, such as receipt contents or how signatures are checked, ask the owner of the ticket.
