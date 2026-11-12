import { randomBytes } from "node:crypto";
import { chmodSync, existsSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { dirname, isAbsolute, join } from "node:path";
import { pathToFileURL } from "node:url";

export function githubProfile(env) {
  for (const name of ["CAO_GITHUB_CLIENT_ID", "CAO_GITHUB_CLIENT_SECRET"]) {
    if (!env[name]?.trim() || ["local-client", "local-client-secret"].includes(env[name])) {
      throw new Error(`${name} requires real GitHub.com OAuth credentials`);
    }
  }
  const organizations = (env.CAO_GITHUB_ALLOWED_ORGS ?? "").split(",").map((org) => org.trim());
  if (!organizations.length || organizations.some((org) => !/^[a-z\d](?:[a-z\d-]{0,37}[a-z\d])?$/i.test(org))) {
    throw new Error("CAO_GITHUB_ALLOWED_ORGS requires explicit GitHub organization logins");
  }
  const port = env.AZURE_LOCAL_FUNCTIONS_PORT ?? "7071";
  if (!/^\d+$/.test(port) || Number(port) < 1024 || Number(port) > 65535) {
    throw new Error("AZURE_LOCAL_FUNCTIONS_PORT must be an unprivileged TCP port (1024–65535)");
  }
  // Browsers allow Secure cookies on HTTP localhost, not numeric loopback.
  const redirect = `http://localhost:${Number(port)}/auth/callback`;
  if (env.CAO_GITHUB_REDIRECT_URL && env.CAO_GITHUB_REDIRECT_URL !== redirect) {
    throw new Error("CAO_GITHUB_REDIRECT_URL must exactly match the localhost callback and selected port");
  }
  if (env.CAO_SESSION_SECRET !== undefined && (!env.CAO_SESSION_SECRET.trim() || env.CAO_SESSION_SECRET.length < 32)) {
    throw new Error("CAO_SESSION_SECRET must contain at least 32 characters");
  }
  const source = env.AZURE_LOCAL_SOURCE_DIRECTORY;
  if (source !== undefined && (!isAbsolute(source) || !existsSync(source) || !statSync(source).isDirectory())) {
    throw new Error("AZURE_LOCAL_SOURCE_DIRECTORY must be an existing absolute snapshot directory");
  }
  return {
    port: String(Number(port)),
    redirect,
    organizations: organizations.join(","),
    sessionSecret: env.CAO_SESSION_SECRET ?? randomBytes(32).toString("hex"),
  };
}

export function validateEnvironment(env) {
  const allowed = new Set([
    "CAO_GITHUB_CLIENT_ID", "CAO_GITHUB_CLIENT_SECRET", "CAO_GITHUB_ALLOWED_ORGS",
    "CAO_GITHUB_REDIRECT_URL", "CAO_SESSION_SECRET",
  ]);
  if (Object.keys(env).some((name) => name.startsWith("CAO_") && !allowed.has(name))) {
    throw new Error("GitHub development accepts only the documented CAO OAuth variables; unset other CAO configuration");
  }
  if (env.GITHUB_SERVER_URL && env.GITHUB_SERVER_URL !== "https://github.com") {
    throw new Error("GitHub development supports GitHub.com only");
  }
  return githubProfile(env);
}

export function writeSettings(path, profile, env) {
  if (profile !== "github") return;
  const config = githubProfile(env);
  const settings = JSON.parse(readFileSync(path, "utf8"));
  Object.assign(settings.Values, {
    CAO_GITHUB_CLIENT_ID: env.CAO_GITHUB_CLIENT_ID,
    CAO_GITHUB_CLIENT_SECRET: env.CAO_GITHUB_CLIENT_SECRET,
    CAO_GITHUB_ALLOWED_ORGS: config.organizations,
    CAO_GITHUB_REDIRECT_URL: config.redirect,
    CAO_SESSION_SECRET: config.sessionSecret,
    CAO_AZURE_DASHBOARD_QUERIES: join(dirname(path), "site/dashboard.json"),
  });
  chmodSync(path, 0o600);
  writeFileSync(path, `${JSON.stringify(settings, null, 2)}\n`, { mode: 0o600 });
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    if (process.argv[2] === "validate") validateEnvironment(process.env);
    else if (process.argv[2] === "settings") writeSettings(process.argv[3], process.argv[4], process.env);
    else throw new Error("unknown Azure local profile command");
  } catch (error) {
    // Validation errors name the setting, never its secret value.
    console.error(`azure-local: ${error.message}`);
    process.exitCode = 1;
  }
}
