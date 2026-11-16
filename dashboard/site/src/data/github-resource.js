/** @typedef {import('./github-service.js').GitHubResource} GitHubResource */

/** @param {GitHubResource} resource */
export function githubResourcePath(resource) {
  if (!resource || !/^[a-z\d](?:[a-z\d-]{0,38})$/i.test(resource.owner)
      || !/^[a-z\d_.-]{1,100}$/i.test(resource.repo)
      || resource.repo === '.' || resource.repo === '..'
      || (resource.resource !== undefined && !['repository', 'latest-release'].includes(resource.resource))) {
    throw new TypeError('Invalid GitHub resource.');
  }
  return `repos/${resource.owner.toLowerCase()}/${resource.repo.toLowerCase()}${resource.resource === 'latest-release' ? '/releases/latest' : ''}`;
}
