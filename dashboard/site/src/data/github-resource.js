import { createDebug } from '../debug.js';

/** @typedef {import('./github-service.js').GitHubResource} GitHubResource */

const debugGitHubResource = createDebug('github-resource');

/** @param {GitHubResource} resource */
export function githubResourcePath(resource) {
  if (!resource || !/^[a-z\d](?:[a-z\d-]{0,38})$/i.test(resource.owner)
      || !/^[a-z\d_.-]{1,100}$/i.test(resource.repo)
      || resource.repo === '.' || resource.repo === '..'
      || (resource.resource !== undefined && !['repository', 'latest-release'].includes(resource.resource))) {
    debugGitHubResource({ event: 'path-rejected', status: 'invalid-resource' });
    throw new TypeError('Invalid GitHub resource.');
  }
  const latestRelease = resource.resource === 'latest-release';
  debugGitHubResource({ event: 'path-resolved', status: 'resolved', resource: latestRelease ? 'latest-release' : 'repository' });
  return `repos/${resource.owner.toLowerCase()}/${resource.repo.toLowerCase()}${latestRelease ? '/releases/latest' : ''}`;
}
