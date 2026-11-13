export function buildCIReport(suites, jobs, url, sha) {
  const results = Object.entries(suites).map(([name, suite]) => ({ name, result: suite.result }));
  const timedJobs = jobs.filter((job) =>
    job.status === "completed" && job.conclusion !== "skipped" &&
    job.started_at && job.completed_at).map((job) => ({
    name: job.name,
    result: job.conclusion,
    seconds: Math.max(0, (Date.parse(job.completed_at) - Date.parse(job.started_at)) / 1000),
    url: job.html_url,
  })).sort((a, b) => b.seconds - a.seconds);
  const status = results.length > 0 && results.every((suite) => suite.result === "success") &&
    timedJobs.length > 0 && timedJobs.every((job) => Number.isFinite(job.seconds) && job.result === "success")
    ? "success" : "incomplete";
  const runnerMinutes = timedJobs.reduce((sum, job) => sum + job.seconds, 0) / 60;
  const escape = (text) => String(text).replaceAll("|", "\\|").replaceAll("\n", " ");
  const markdown = [
    "# Daily full validation",
    "",
    `**Result: ${status}.** Commit: \`${sha}\`. [Run and evidence](${url}).`,
    "",
    "| Suite | Result |",
    "| --- | --- |",
    ...results.map((suite) => `| ${escape(suite.name)} | ${escape(suite.result)} |`),
    "",
    `Completed jobs consumed **${runnerMinutes.toFixed(1)} runner-minutes** (not billing-weighted; excludes queue time and this report).`,
    "",
    "## Slowest jobs",
    "",
    "| Job | Result | Seconds |",
    "| --- | --- | ---: |",
    ...timedJobs.slice(0, 20).map((job) =>
      `| [${escape(job.name)}](${job.url}) | ${escape(job.result)} | ${job.seconds.toFixed(0)} |`),
    "",
    "Skipped, cancelled, or failed suites and missing job evidence are incomplete, never a clean daily pass. Detailed logs, traces, profiles, and benchmark reports are attached to this run.",
    "",
  ].join("\n");
  return { status, sha, url, suites: results, runnerMinutes, jobs: timedJobs, markdown };
}

export async function reportCIFailure(github, repo, report) {
  if (report.status === "success") return;
  const marker = "<!-- cao-daily-validation-failure -->";
  const title = "Daily full validation failed";
  const body = `${marker}\n\n${report.markdown}`;
  const issues = await github.paginate(github.rest.issues.listForRepo, {
    ...repo, state: "open", creator: "github-actions[bot]", per_page: 100,
  });
  const existing = issues.find((issue) =>
    !issue.pull_request && issue.body?.includes(marker));
  if (existing) {
    await github.rest.issues.update({ ...repo, issue_number: existing.number, body });
  } else {
    await github.rest.issues.create({ ...repo, title, body });
  }
}
