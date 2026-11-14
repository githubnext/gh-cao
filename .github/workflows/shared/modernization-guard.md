---
safe-outputs:
  needs: [modernization_guard]
jobs:
  modernization_guard:
    runs-on: ubuntu-latest
    needs: agent
    permissions:
      actions: read
      contents: read
    steps:
      - name: Checkout immutable modernization publication guard
        uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          ref: ${{ github.workflow_sha }}
          persist-credentials: false
          path: .modernization-output
      - name: Restore modernization agent output for validation
        uses: actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c # v8.0.1
        with:
          pattern: "{agent,agent-output-fallback}"
          merge-multiple: true
          path: /tmp/gh-aw/
      - name: Enforce trusted modernization publication
        uses: actions/github-script@3a2844b7e9c422d3c10d287c895573f7108da1b3 # v9.0.0
        env:
          MODERNIZATION_SOURCE: ${{ github.workspace }}/.modernization-output
          GITHUB_WORKFLOW_SHA: ${{ github.workflow_sha }}
          GH_AW_AGENT_OUTPUT: /tmp/gh-aw/agent_output.json
          CAO_AUTH_MODE: ${{ vars.GH_AW_GITHUB_AUTH_MODE }}
          CAO_CONTROL_READ_TOKEN: ${{ github.token }}
          CAO_READ_APP_ID: ${{ (vars.GH_AW_GITHUB_AUTH_MODE == 'app' || vars.GH_AW_GITHUB_AUTH_MODE == '') && vars.GH_AW_GITHUB_READ_APP_ID || '' }}
          CAO_READ_APP_PRIVATE_KEY: ${{ (vars.GH_AW_GITHUB_AUTH_MODE == 'app' || vars.GH_AW_GITHUB_AUTH_MODE == '') && secrets.GH_AW_GITHUB_READ_APP_PRIVATE_KEY || '' }}
          CAO_READ_PAT_REPOSITORIES: ${{ vars.GH_AW_GITHUB_READ_PAT_REPOSITORIES }}
          CAO_CONTROL_READ_PAT: ${{ vars.GH_AW_GITHUB_AUTH_MODE == 'pat' && secrets[fromJSON(vars.GH_AW_GITHUB_READ_PAT_REPOSITORIES || '{}')[github.repository]] || '' }}
          CAO_INVENTORY_PAT_REPOSITORY: ${{ inputs.target_repo }}
          CAO_INVENTORY_READ_PAT: ${{ vars.GH_AW_GITHUB_AUTH_MODE == 'pat' && secrets[fromJSON(vars.GH_AW_GITHUB_READ_PAT_REPOSITORIES || '{}')[inputs.target_repo]] || '' }}
          CAO_LEGACY_READ_TOKEN: ${{ vars.GH_AW_GITHUB_AUTH_MODE == '' && (secrets.GH_AW_GITHUB_READ_PAT || secrets.GH_AW_GITHUB_TOKEN) || '' }}
          CAO_WORKFLOW_TOKEN: ${{ github.token }}
        with:
          script: |
            const { pathToFileURL } = require('node:url');
            const runtime = await import(pathToFileURL(`${process.env.MODERNIZATION_SOURCE}/modernization/actions.mjs`).href);
            if (process.env.CAO_ROLE === 'orchestrator') {
              await runtime.guardDispatchOutputs(github);
            } else if (process.env.CAO_ROLE === 'worker') {
              await runtime.guardWorkerOutputs({ github, core, getOctokit });
            } else {
              throw new Error('Modernization publication requires an admitted coordinator or task worker');
            }
---
