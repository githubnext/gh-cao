import { h } from '../dom.js';
import { render, state } from '../reactive.js';
import { isSafeGithubUrlBase, isSafeRepositorySlug } from '../validator-common.js';
import { createFactoryScope } from './factory-elements.js';

const GH_AW_AUTH = 'https://github.github.com/gh-aw/reference/auth/';
const CAO_AUTH = 'https://github.com/githubnext/gh-cao/blob/main/docs/authentication.md';
const APP_INFERENCE = 'https://docs.github.com/en/copilot/how-tos/copilot-sdk/auth/server-to-server-tokens';
const BROKER_SETUP = 'https://github.com/githubnext/gh-cao/blob/main/server/SECURITY.md#optional-copilot-requests-broker';

/** @param {string} href @param {string} label */
const link = (href, label) => h('a', { href, target: '_blank', rel: 'noopener noreferrer' }, label);

/** @param {string} title @param {...HTMLElement} body */
const section = (title, ...body) => h('details', null, h('summary', null, title), ...body);

/** @param {string} repositoryUrl @param {string} name */
const secret = (repositoryUrl, name) => link(`${repositoryUrl}/settings/secrets/actions/new`, `Add ${name} in GitHub`);

/** @param {string} repositoryUrl */
function copilotSetup(repositoryUrl) {
  return section('GitHub Copilot',
    section('Organization billing (no secret)',
      h('p', null, 'Use an organization-owned repository with Copilot billing enabled. The reviewed workflow must grant ',
        h('code', null, 'copilot-requests: write'), ' to use its built-in Actions token.'),
      link(`${GH_AW_AUTH}#copilot-requests-write-permission`, 'Org billing requirements')),
    section('Fine-grained personal access token',
      h('p', null, 'For a custom gh-aw workflow approved for PAT-based inference, create a fine-grained PAT with the Copilot Requests account permission set to Read. The workflow must omit ',
        h('code', null, 'copilot-requests: write'), '. Bundled CAO workflows do not fall back to a PAT.'),
      h('p', null, secret(repositoryUrl, 'COPILOT_GITHUB_TOKEN'), ' · ', link(`${GH_AW_AUTH}#copilot_github_token`, 'PAT setup requirements'))),
    section('GitHub App for inference',
      h('p', null, 'Use a dedicated organization App with Copilot Requests: Read for a compatible server-to-server integration. An installation token is not a valid ',
        h('code', null, 'COPILOT_GITHUB_TOKEN'), '. Do not reuse the read-only repository App for inference. CAO’s App-backed path is the token broker below.'),
      link(APP_INFERENCE, 'GitHub App inference requirements')),
    section('CAO token broker',
      h('p', null, 'Ask the hosting operator to enable the HTTPS broker, keep the dedicated App key in Azure Key Vault, and enroll the exact workflow revision. This is not a repository secret. Use only a compatible gh-aw consumer and do not also enable built-in org billing permissions. Main-agent inference requires separate renewable credential support.'),
      link(BROKER_SETUP, 'Token broker setup and limitations')));
}

/** @param {string} engine @param {string} repositoryUrl */
function engineSetup(engine, repositoryUrl) {
  if (engine === 'copilot') return copilotSetup(repositoryUrl);
  if (engine === 'claude') return section('Anthropic (Claude Code)',
    h('p', null, 'For API-key authentication, create an Anthropic key and save it under ',
      h('code', null, 'ANTHROPIC_API_KEY'), ' unless the reviewed workflow references a different name. Workload identity configurations may not require a key.'),
    h('p', null, secret(repositoryUrl, 'ANTHROPIC_API_KEY'), ' · ', link(`${GH_AW_AUTH}#anthropic_api_key`, 'Anthropic setup requirements')));
  if (engine === 'codex') return section('OpenAI (Codex)',
    h('p', null, 'For API-key authentication, create an OpenAI key and save it under ',
      h('code', null, 'OPENAI_API_KEY'), ' unless the reviewed workflow references a different name. Workload identity configurations may not require a key.'),
    h('p', null, secret(repositoryUrl, 'OPENAI_API_KEY'), ' · ', link(`${GH_AW_AUTH}#openai_api_key`, 'OpenAI setup requirements')));
  return section(engine, h('p', null, 'No dedicated setup is available for this engine. Check the reviewed workflow for its credential references.'),
    link(GH_AW_AUTH, 'gh-aw authentication reference'));
}

/** @param {string} repositoryUrl */
function githubReadSetup(repositoryUrl) {
  return section('Read-only GitHub App access',
    h('p', null, 'Use a private App with only the required read permissions. Install it on the exact repositories it must inspect. In the repository running the workflow, create the Actions variable ',
      h('code', null, 'GH_AW_GITHUB_READ_APP_ID'), ' with the App client ID and store the PEM as ',
      h('code', null, 'GH_AW_GITHUB_READ_APP_PRIVATE_KEY'), '.'),
    h('p', null, 'Verify the workflow binds these values for read-only GitHub tools (for example via ',
      h('code', null, 'tools.github.github-app'), '). For shared CAO control workflows, review the control-plane App authentication profile before changing its mode. Do not use this App for safe-output writes or inference.'),
    h('p', null, link(`${repositoryUrl}/settings/variables/actions`, 'Open Actions variables'), ' · ',
      secret(repositoryUrl, 'GH_AW_GITHUB_READ_APP_PRIVATE_KEY'), ' · ',
      link(`${CAO_AUTH}#runtime-credential-selection`, 'CAO read/write credential separation')));
}

/** @param {import('./ui-elements.js').ElementRenderContext} context */
export function renderInferenceCredentials(context) {
  const source = context.sources[context.sourceNames[0]];
  const heading = h(context.headingTag, null, context.title);
  const root = h('section', { 'aria-label': context.title, className: 'dashboard-section' }, heading);
  const base = context.githubUrlBase ?? 'https://github.com';
  if (!isSafeGithubUrlBase(base)) {
    root.append(h('p', null, 'GitHub settings are unavailable: the configured GitHub URL is invalid.'));
    return root;
  }
  if (!source || source.metadata?.availability === 'unavailable') {
    root.append(h('p', null, 'Workflow engine inventory is unavailable. Refresh inventory and retry.'));
    return root;
  }
  if (source.metadata?.completeness !== 'complete' || source.metadata?.freshness === 'stale') {
    root.append(h('p', null, 'Workflow inventory may be incomplete or out of date. Verify the reviewed workflow before setting credentials.'));
  }
  if (!source.rows.length) {
    root.append(h('p', null, 'No agentic workflows were found in the available inventory.'));
    return root;
  }

  const scope = createFactoryScope();
  const selected = state(source.rows.length === 1 ? 0 : -1);
  const select = h('select', {
    className: 'form-select',
    onchange: (/** @type {Event} */ event) => selected.set(/** @type {HTMLSelectElement} */ (event.currentTarget).selectedIndex - 1)
  }, h('option', { selected: source.rows.length !== 1 }, 'Choose a repository'),
  ...source.rows.map((row, index) => h('option', { selected: source.rows.length === 1 && index === 0 },
    isSafeRepositorySlug(row['repository-coordinate']) ? row['repository-coordinate'] : 'Unavailable repository')));
  const content = h('div');
  root.append(h('label', null, 'Workflow repository ', select), content);
  render(content, () => {
    const row = source.rows[selected.get()];
    if (!row) return h('p', null, 'Choose the repository that runs the workflows.');
    const coordinate = row['repository-coordinate'];
    if (!isSafeRepositorySlug(coordinate) || coordinate.endsWith('/.')) {
      return h('p', null, 'Repository settings are unavailable for this inventory entry.');
    }
    const repositoryUrl = `${base.replace(/\/$/, '')}/${coordinate}`;
    const engines = typeof row.engines === 'string' && row.engines ? row.engines.split(', ') : [];
    return [
      h('p', null, 'Detected engines: ', engines.join(', ') || 'none confirmed', '.'),
      ...(Number(row['unknown-engine-count']) > 0
        ? [h('p', null, `${row['unknown-engine-count']} workflow(s) have no engine metadata. Check the reviewed workflow; do not assume an engine.`)] : []),
      ...engines.map((engine) => engineSetup(engine, repositoryUrl)),
      githubReadSetup(repositoryUrl)
    ];
  }, { signal: scope.signal });
  scope.bind(root);
  return root;
}
