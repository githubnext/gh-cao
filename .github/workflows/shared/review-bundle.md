---
import-schema:
  publication_gate:
    type: string
    default: detection
safe-outputs:
  jobs:
    publish-review-bundle:
      needs: ["${{ github.aw.import-inputs.publication_gate }}"]
      description: "Publish one review bundle as a workflow artifact from a prepared directory"
      runs-on: ubuntu-latest
      artifacts:
        - /tmp/gh-aw/agent/review-bundles/
      output: "Review bundle published as a workflow artifact."
      inputs:
        bundle_name:
          description: "Short bundle name used in the artifact name"
          required: true
          type: string
        source_dir:
          description: "Workspace or /tmp directory containing the prepared review bundle files"
          required: true
          type: string
        target_repo:
          description: "Target repository that would receive the live safe output"
          required: true
          type: string
        requested_output:
          description: "Intended safe-output primitive, such as create-pull-request or upload-asset"
          required: true
          type: string
        base_branch:
          description: "Target base branch when known"
          required: false
          type: string
        base_sha:
          description: "Target base SHA when known"
          required: false
          type: string
        summary:
          description: "One-line human summary of the review bundle"
          required: false
          type: string
      steps:
        - id: prepare
          shell: bash
          run: |
            set -euo pipefail

            if [ ! -f "$GH_AW_AGENT_OUTPUT" ]; then
              echo "skip_upload=true" >> "$GITHUB_OUTPUT"
              echo "No GH_AW_AGENT_OUTPUT file found; skipping review bundle publish."
              exit 0
            fi

            ITEM_JSON=$(jq -c '.items[] | select(.type == "publish_review_bundle")' "$GH_AW_AGENT_OUTPUT" | head -n 1)
            if [ -z "$ITEM_JSON" ]; then
              echo "skip_upload=true" >> "$GITHUB_OUTPUT"
              echo "No publish_review_bundle request found; skipping."
              exit 0
            fi

            BUNDLE_NAME=$(printf '%s' "$ITEM_JSON" | jq -r '.bundle_name')
            SOURCE_DIR_RAW=$(printf '%s' "$ITEM_JSON" | jq -r '.source_dir')
            TARGET_REPO=$(printf '%s' "$ITEM_JSON" | jq -r '.target_repo')
            REQUESTED_OUTPUT=$(printf '%s' "$ITEM_JSON" | jq -r '.requested_output')
            BASE_BRANCH=$(printf '%s' "$ITEM_JSON" | jq -r '.base_branch // ""')
            BASE_SHA=$(printf '%s' "$ITEM_JSON" | jq -r '.base_sha // ""')
            SUMMARY=$(printf '%s' "$ITEM_JSON" | jq -r '.summary // ""')

            if [ -z "$BUNDLE_NAME" ] || [ -z "$SOURCE_DIR_RAW" ] || [ -z "$TARGET_REPO" ] || [ -z "$REQUESTED_OUTPUT" ]; then
              echo "Missing required review bundle fields." >&2
              exit 1
            fi

            PERSISTED_ROOT="/tmp/gh-aw/agent/review-bundles"
            if [[ "$SOURCE_DIR_RAW" != "$PERSISTED_ROOT"/* ]]; then
              echo "source_dir must be under $PERSISTED_ROOT: $SOURCE_DIR_RAW" >&2
              exit 1
            fi

            ARTIFACT_ROOT=$(realpath -m "$(dirname "$GH_AW_AGENT_OUTPUT")")
            SOURCE_SUFFIX=${SOURCE_DIR_RAW#"/tmp/gh-aw/agent/"}
            SOURCE_DIR=$(realpath -m "$ARTIFACT_ROOT/agent/$SOURCE_SUFFIX")
            RESTORED_ROOT=$(realpath -m "$ARTIFACT_ROOT/agent/review-bundles")
            if [[ "$SOURCE_DIR" != "$RESTORED_ROOT"/* ]]; then
              echo "source_dir escapes the restored review bundle root: $SOURCE_DIR_RAW" >&2
              exit 1
            fi

            if [ ! -d "$SOURCE_DIR" ]; then
              echo "::error::Requested review bundle was not persisted in the agent artifact: $SOURCE_DIR_RAW" >&2
              exit 1
            fi

            SAFE_REPO=$(printf '%s' "$TARGET_REPO" | tr '/:' '-' | tr -cs 'A-Za-z0-9._-' '-')
            SAFE_NAME=$(printf '%s' "$BUNDLE_NAME" | tr '/:' '-' | tr -cs 'A-Za-z0-9._-' '-')
            ARTIFACT_NAME="review-${SAFE_REPO}-${SAFE_NAME}"
            STAGING_DIR="$RUNNER_TEMP/review-bundle/${SAFE_REPO}/${SAFE_NAME}"
            mkdir -p "$STAGING_DIR"
            cp -R "$SOURCE_DIR"/. "$STAGING_DIR"/

            cat > "$STAGING_DIR/bundle-manifest.json" <<EOF
            {
              "target_repo": $(printf '%s' "$TARGET_REPO" | jq -Rs .),
              "requested_output": $(printf '%s' "$REQUESTED_OUTPUT" | jq -Rs .),
              "base_branch": $(printf '%s' "$BASE_BRANCH" | jq -Rs .),
              "base_sha": $(printf '%s' "$BASE_SHA" | jq -Rs .),
              "summary": $(printf '%s' "$SUMMARY" | jq -Rs .),
              "workflow": $(printf '%s' "$GITHUB_WORKFLOW" | jq -Rs .),
              "run_id": $(printf '%s' "$GITHUB_RUN_ID" | jq -Rs .),
              "run_attempt": $(printf '%s' "$GITHUB_RUN_ATTEMPT" | jq -Rs .)
            }
            EOF

            cat > "$STAGING_DIR/README.md" <<EOF
            # Review Bundle

            - Target repository: $TARGET_REPO
            - Intended safe output: $REQUESTED_OUTPUT
            - Base branch: ${BASE_BRANCH:-unknown}
            - Base SHA: ${BASE_SHA:-unknown}
            - Source workflow: $GITHUB_WORKFLOW
            - Source run: $GITHUB_SERVER_URL/$GITHUB_REPOSITORY/actions/runs/$GITHUB_RUN_ID

            ${SUMMARY:-No additional summary was provided.}
            EOF

            if [ "${GH_AW_SAFE_OUTPUTS_STAGED:-false}" = "true" ]; then
              {
                echo "## Review Bundle Preview"
                echo
                echo "- Artifact name: $ARTIFACT_NAME"
                echo "- Target repository: $TARGET_REPO"
                echo "- Intended safe output: $REQUESTED_OUTPUT"
                echo "- Bundle directory: $SOURCE_DIR"
              } >> "$GITHUB_STEP_SUMMARY"
              echo "skip_upload=true" >> "$GITHUB_OUTPUT"
              exit 0
            fi

            {
              echo "artifact_name=$ARTIFACT_NAME"
              echo "bundle_path=$STAGING_DIR"
              echo "skip_upload=false"
            } >> "$GITHUB_OUTPUT"

        - name: Upload review bundle artifact
          if: steps.prepare.outputs.skip_upload != 'true'
          uses: actions/upload-artifact@v7.0.1
          with:
            name: ${{ steps.prepare.outputs.artifact_name }}
            path: ${{ steps.prepare.outputs.bundle_path }}
            retention-days: 14
---