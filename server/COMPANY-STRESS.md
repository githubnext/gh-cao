# Configurable company ingestion stress

`npm run test:stress:company -- --preset large` generates synthetic AW evidence
and measures real hash-verified ingestion, PostgreSQL retention, and HTTP queries.
It skips agents, AW execution/completion, GitHub calls, webhooks, and collection
subprocesses. Synthetic completed-run records approximate the workload; this is
not an end-to-end execution or live GitHub throughput benchmark.

Provide `POSTGRES_URL` for **disposable local PostgreSQL on numeric loopback**.
The test creates and removes its own schema, including operational tables; it
does not rebuild another namespace. Enterprise cases need adequate PostgreSQL
lock-table headroom: the manual workflow starts its disposable instance with
`postgres -c max_locks_per_transaction=1024`. This changes test infrastructure,
not CAO storage/query limits. No Redis or model credentials are needed.

```sh
POSTGRES_URL='postgres://postgres@127.0.0.1:5432/cao_test?sslmode=disable' \
  npm run test:stress:company -- --preset large

# Override any assumption; developer/repository counts need not divide evenly.
POSTGRES_URL='postgres://postgres@127.0.0.1:5432/cao_test?sslmode=disable' \
  npm run test:stress:company -- --preset small \
    --repositories 7 --developers 3 --prs-per-developer-per-day 2 \
    --aw-runs-per-pr 4 --days 2 --aic-per-run 0.25 \
    --seed 1 --distribution burst --timeout 5m
```

Presets in `tests/fixtures/company-stress/` cover small (10 repositories and
developers), medium (1,000 each), and large (10,000 each) companies. All default
to one day, 10 PRs/developer/day, five AW runs/PR, and **100 modeled AIC/run**.
The large preset produces **100,000 PRs, 500,000 AW records, and 50 million modeled
AIC per day**. Repository count distributes developer activity; it is not another
volume multiplier. PRs and developers are logical generator identities, not new
canonical entities. AIC is neither measured spending nor a currency conversion.

Use `--scenario FILE` instead of `--preset` for a complete version-1 JSON scenario,
then apply the same overrides. Counts are integers; PR rate, AW fan-out, and AIC
may be zero. Repositories are bounded to 50,000, developers to 1 million, duration
to 31 days, PR rate/fan-out to 1,000 each, and total PRs/AW records to 5 million
each. Oversized scenarios fail explicitly rather than sampling. `uniform` and
`burst` describe fixture timestamps, not paced traffic or measured peak capacity.

Evidence streams into a single snapshot using production bounded ingestion
batches; the generator does not allocate the complete corpus. The ordinary
200,000-row hot-detail ceiling and 30-day cutoff remain in effect. Reports
separate generated/ingested volume from retained and evicted/expired records and
compare retained AIC only to retained runs. Repeated identical ingestion must
preserve the revision. No production cap is raised to keep the whole fixture.

`test-results/company-stress/company-stress.json` records configuration, modeled
totals, stage status, measured generation/ingestion durations and records/second,
three HTTP query timings/results, and the post-ingestion Go heap checkpoint (not
peak RSS or database memory). `--report-dir PATH` selects another directory.
Failure reports identify the incomplete stage; the command exits nonzero. A hard
timeout may leave only the launcher's incomplete report and test log.

The existing Go CI runs the small service-backed smoke test. Daily full validation
(`.github/workflows/ci-daily.yml`) runs the large preset on `main` at 06:37 UTC
and includes its outcome in the aggregate daily report. The reusable
`.github/workflows/company-stress.yml` also accepts manual preset or complete
scenario JSON inputs and uploads evidence even on failure. Existing fleet,
backfill, and monthly-budget tests retain their separate coverage.

The dashboard's **Simulators > Pull request** tab is a side-effect-free calculator
for the same assumptions. It does not launch this runner.
