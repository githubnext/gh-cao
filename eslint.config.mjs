import noHardcodedGitHubActionsUrl from "./eslint-rules/no-hardcoded-github-actions-url.mjs";

export default [
  {
    ignores: [
      ".cao-dashboard-preview-*/**",
      ".tmp/**",
      "dashboard/site/**",
      "node_modules/**",
      "server/internal/dashboardassets/resources/site/**",
      "test-results/**",
      "tests/**",
    ],
  },
  {
    files: ["**/*.{js,mjs,cjs}"],
    plugins: {
      cao: {
        rules: {
          "no-hardcoded-github-actions-url": noHardcodedGitHubActionsUrl,
        },
      },
    },
    rules: {
      "cao/no-hardcoded-github-actions-url": "error",
    },
  },
];
