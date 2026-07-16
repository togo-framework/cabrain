---
name: togo-resource
description: Scaffold a full resource (model + migration + REST + GraphQL + page) in a togo app.
---

# togo-resource

Run `togo make:resource <Name> field:type ...` then `togo generate` and `togo migrate`.
Fields support `name:type[:nullable]`. The resource is exposed over REST + GraphQL with a page.
