# Glossary

Terms used in the Astrolift CLI documentation.

- **agent**: a registered workload of kind `agent` that `astro agent dispatch` runs as a batch run or in a box.
- **app**: the deployable unit, managed with `astro app` and registered under a project.
- **brain**: a Mimwell semantic layer, registered as an app of kind `mimwell` and named by its app slug.
- **deployment**: one rollout of an app, created by `astro ci deploy` and read with `astro ci status <deployment-id>`.
- **environment**: a deploy target of an app, which workloads and secrets are scoped to.
- **org**: the account that owns teams, projects and apps, chosen with `--org` or `astro org use`.
- **project**: a group of apps within an org.
- **team**: a group of people within an org, which can be given roles.
- **workflow**: a TOML definition of stages that you author, validate and run with `astro workflow`.
