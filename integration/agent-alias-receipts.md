# Historical agent alias acceptance

`test_agent_definition_alias.py` uses the same opt-in backend environment as
[pipeline acceptance](pipeline-receipts.md). Build this checkout and set
`ASTROLIFT_CLI_TEST_BINARY` to its absolute binary path. Run through the backend's
pytest configuration with a separate PostgreSQL database and its migrations.
The forwarding proxy faults transport only; API schema, bearer authorization,
credential ceilings, encrypted durable request storage, Temporal SDK client,
production Definition workflow and checkpoint activities remain real.

The case dispatches `astro agent run <definition-GUID> --request-file <path>
--input @<file> --yes --json`, drops the actual accepted response and verifies
its exact organization, actor, revision, schema digest and saved request key.
It deletes the input file and changes the definition before recovering the
original execution through metadata reads without another dispatch. A real
Temporal worker completes the frozen checkpoint workflow; `--wait` observes
that original engine run and emits one result containing metadata only.

Slugs, new literal inputs, insufficient token scope and an actual schema
validation error are refused without the legacy mutation. Inputs and bearer
credentials never enter the saved request file or CLI receipts. This case uses
one checkpoint stage; it does not certify external agent execution, Kubernetes
resource cleanup or every stage type. Python acceptance runs on the local
POSIX host. Windows runtime behavior requires the separate native Windows CI
private-file and command-consumer gate.
