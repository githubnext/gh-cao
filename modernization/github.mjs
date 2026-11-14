import { taskComplete, taskMarker } from "./engagement.mjs";

export async function boundedPages(client, route, parameters = {}) {
  const rows = [];
  for (let page = 1; page <= 10; page++) {
    const response = await client.request(`GET ${route}`, { ...parameters, per_page: 100, page });
    const data = response.data.check_runs ?? response.data;
    if (!Array.isArray(data)) throw new Error("Modernization: invalid GitHub list response");
    rows.push(...data);
    if (!response.headers?.link?.includes('rel="next"')) return rows;
  }
  throw new Error("Modernization: incomplete GitHub pagination");
}

export async function collectEvidence(plan, digest, candidates, inventory, control, centralRepository) {
  const evidence = {};
  const byRepository = new Map(candidates.map(candidate => [candidate.full_name.toLowerCase(), candidate]));
  for (const repository of plan.repositories) {
    const candidate = byRepository.get(repository.repository.toLowerCase());
    if (!candidate) throw new Error("Modernization: the entire engagement must fit the current admitted inventory");
    const name = repository.repository;
    const metadata = (await inventory.request(`GET /repos/${name}`)).data;
    if (metadata.default_branch !== repository.base_branch) throw new Error("Modernization: default branch changed; revise the plan");
    const base = (await inventory.request(`GET /repos/${name}/commits/{ref}`, { ref: repository.base_branch })).data.sha;
    const comparison = (await inventory.request(`GET /repos/${name}/compare/{basehead}`, {
      basehead: `${repository.base_sha}...${base}`,
    })).data;
    if (!["ahead", "identical"].includes(comparison.status)) throw new Error("Modernization: approved baseline is not an ancestor");
    const destination = candidate.safe_output_repo;
    const issueClient = destination.toLowerCase() === centralRepository.toLowerCase() ? control : inventory;
    const issues = await boundedPages(issueClient, `/repos/${destination}/issues`, { state: "all", labels: "modernization" });
    const pulls = await boundedPages(inventory, `/repos/${name}/pulls`, { state: "all", sort: "updated", direction: "desc" });
    for (const task of plan.tasks.filter(task => task.repository.toLowerCase() === name.toLowerCase())) {
      const marker = taskMarker(digest, task.id);
      const taskIssues = issues.filter(issue => !issue.pull_request && issue.user?.type === "Bot"
        && issue.body?.includes(marker) && issue.body.includes("<!-- cao-modernization-phase:issue -->"));
      const proposals = issues.filter(issue => !issue.pull_request && issue.user?.type === "Bot"
        && issue.body?.includes(marker) && issue.body.includes("<!-- cao-modernization-phase:implement -->"));
      const taskPulls = pulls.filter(pull => pull.user?.type === "Bot" && pull.body?.includes(marker));
      if (taskIssues.length > 1 || taskPulls.length > 1) throw new Error("Modernization: ambiguous duplicate task outputs");
      const issue = taskIssues[0];
      let complete = false;
      if (taskPulls[0]?.merged_at) {
        const pull = (await inventory.request(`GET /repos/${name}/pulls/{number}`, { number: taskPulls[0].number })).data;
        if (pull.base.ref !== repository.base_branch) throw new Error("Modernization: task merged into the wrong branch");
        const reviews = await boundedPages(inventory, `/repos/${name}/pulls/${pull.number}/reviews`);
        const checks = await boundedPages(inventory, `/repos/${name}/commits/${pull.head.sha}/check-runs`, { filter: "latest" });
        const status = (await inventory.request(`GET /repos/${name}/commits/${pull.head.sha}/status`)).data;
        if (status.total_count !== status.statuses?.length) throw new Error("Modernization: incomplete commit status evidence");
        complete = taskComplete(pull, reviews, checks, status.statuses);
      }
      evidence[task.id] = {
        complete, issue: issue?.number ?? null, closed_issue: issue?.state === "closed",
        active_pull: Boolean(taskPulls.length || proposals.length), base_sha: base,
      };
    }
  }
  return evidence;
}
