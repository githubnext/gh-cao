const MAX_MANIFEST_BYTES = 256 * 1024;

function repositoryLink(repository, apiUrl) {
  try {
    const api = new URL(apiUrl);
    if (api.protocol !== "https:" || api.username || api.password) return undefined;
    let webBase;
    if (api.hostname === "api.github.com" && api.pathname === "/") {
      webBase = "https://github.com";
    } else if (/\/api\/v3\/?$/.test(api.pathname)) {
      webBase = `${api.origin}${api.pathname.replace(/\/api\/v3\/?$/, "")}`;
    } else {
      return undefined;
    }
    const path = repository.split("/").map(encodeURIComponent).join("/");
    return {
      relation: "repository",
      href: `${webBase.replace(/\/$/, "")}/${path}`,
      label: `Open ${repository} on GitHub`,
    };
  } catch {
    return undefined;
  }
}

function scalar(source, name) {
  const value = source.match(new RegExp(`^${name}:[ \\t]*(.+?)\\s*$`, "m"))?.[1]?.trim() ?? "";
  if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) {
    return value.slice(1, -1);
  }
  return value;
}

function includes(source) {
  const block = source.match(/^includes:\s*\n((?:^[ \t]+.*\n?)*)/m)?.[1] ?? "";
  return block.split(/\r?\n/)
    .map((line) => line.match(/^\s*-\s+(?:source:\s+)?([^#]+?)\s*$/)?.[1])
    .filter(Boolean)
    .map((value) => value.replace(/^['"]|['"]$/g, ""));
}

export function parsePackageManifest(source, coordinates) {
  if (Buffer.byteLength(source) > MAX_MANIFEST_BYTES) throw new Error("package manifest exceeds size limit");
  const workflow = coordinates.format === "workflows";
  let body = "";
  if (workflow) {
    const document = source.replace(/\r\n/g, "\n").match(/^---\n([\s\S]*?)\n---(?:\n|$)([\s\S]*)$/);
    if (!document || !/^on[ \t]*:/m.test(document[1])) return null;
    source = document[1];
    body = document[2].trim();
    if (scalar(source, "private") === "true") return null;
  }
  const readme = typeof coordinates.readme === "string" ? coordinates.readme : "";
  const readmePath = readme ? String(coordinates.readmePath ?? "") : workflow && body ? coordinates.path : "";
  const name = scalar(source, "name") || (workflow
    ? body.match(/^#\s+(.+)$/m)?.[1]?.trim() || coordinates.path.split("/").at(-1).replace(/\.md$/, "")
    : "");
  if (!name) throw new Error("package manifest name is required");
  const contents = workflow ? [coordinates.path] : includes(source);
  const version = scalar(source, "version") || coordinates.ref;
  const packagePath = workflow ? coordinates.path : coordinates.path.replace(/\/?aw\.yml$/, "");
  const sourceCoordinate = `${coordinates.repository}${packagePath ? `/${packagePath}` : ""}@${coordinates.resolvedCommit}`;
  let description = scalar(source, "description");
  if (workflow && /^[|>][-+]?$/.test(description)) {
    const lines = source.match(/^description:[ \t]*[|>][-+]?[ \t]*\n((?:[ \t]+[^\n]*(?:\n|$)|\n)*)/m)?.[1] ?? "";
    description = lines.trim().split("\n").map((line) => line.trim()).join(description.startsWith(">") ? " " : "\n");
  }
  return {
    id: `${coordinates.repository}${packagePath ? `/${packagePath}` : ""}`.toLowerCase(),
    "registry-id": coordinates.registryId,
    "registry-name": coordinates.registryName,
    "registry-precedence": coordinates.precedence,
    name,
    description,
    publisher: coordinates.repository.split("/")[0],
    repository: coordinates.repository,
    "repository-link": repositoryLink(coordinates.repository, coordinates.apiUrl ?? "https://api.github.com"),
    path: packagePath,
    ref: coordinates.ref,
    "resolved-commit": coordinates.resolvedCommit,
    version,
    icon: scalar(source, "icon").replace(/^:([a-z0-9]+(?:-[a-z0-9]+)*):$/, "$1") || "workflow",
    artwork: scalar(source, "artwork") || "",
    contents,
    readme: readme || (workflow ? body : ""),
    "readme-path": readmePath,
    source: sourceCoordinate,
    "add-command": `${workflow ? "gh aw" : "gh cao"} add ${sourceCoordinate}`,
    stars: coordinates.repositoryCounts?.stars ?? null,
    forks: coordinates.repositoryCounts?.forks ?? null,
  };
}
