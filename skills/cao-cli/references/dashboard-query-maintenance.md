# Dashboard query maintenance

Use this reference only when adding or changing Dashboard Language queries.

1. Measure the affected query and the full query graph:

   ```bash
   cao dashboard-complexity \
     --input dashboard/site/dashboard.json \
     --postgres-url "$CAO_POSTGRES_URL"
   ```

   Pass a query ID before the options to inspect one query. Use
   `--format markdown` for review output and `--limit COUNT` to bound it.
2. Compare direct reads, dependency reads, source coefficients, and pressure
   rank before and after the change. A declarative query is not automatically
   cheap.
3. Find reuse opportunities:

   ```bash
   cao prune-dashboard --input dashboard/site/dashboard.json
   ```

4. Review exact and compatible duplicates, shared chains, unreferenced queries,
   retained views, and the final inventory before writing output.
5. Only after review, pass `--output FILE`. Preserve public query names and use
   subject-based names for extracted bases; never use numeric placeholders.

Prefer an existing base query, narrower source, earlier filter, fewer joins, or
reusable intermediate aggregation. Explain any intentional cost increase.
