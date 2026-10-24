-- Apply separately after canonical bootstrap. Never grant ALL TABLES or
-- default table privileges: canonical and operational storage may share a schema.
BEGIN;
REVOKE CREATE ON SCHEMA :"schema" FROM PUBLIC, :"runtime";
GRANT USAGE ON SCHEMA :"schema" TO :"runtime";
REVOKE ALL ON :"schema".cao_contract, :"schema".cao_state FROM PUBLIC, :"runtime";
GRANT SELECT ON :"schema".cao_contract TO :"runtime";
GRANT SELECT, INSERT, UPDATE ON :"schema".cao_state TO :"runtime";
REVOKE ALL ON :"schema".cao_daily_contract, :"schema".daily_run_counts,
    :"schema".run_count_contributions, :"schema".daily_run_count_state FROM PUBLIC, :"runtime";
GRANT SELECT ON :"schema".cao_daily_contract TO :"runtime";
GRANT SELECT, INSERT, UPDATE, DELETE ON :"schema".daily_run_counts TO :"runtime";
GRANT SELECT, INSERT, UPDATE, DELETE ON :"schema".run_count_contributions TO :"runtime";
GRANT SELECT, INSERT, UPDATE ON :"schema".daily_run_count_state TO :"runtime";
REVOKE ALL ON :"schema".cao_value_publications,
    :"schema".operational_value_evidence, :"schema".operational_value_evidence_facts
    FROM PUBLIC, :"runtime";
GRANT SELECT, INSERT, UPDATE ON :"schema".cao_value_publications TO :"runtime";
GRANT SELECT, INSERT, DELETE ON
    :"schema".operational_value_evidence, :"schema".operational_value_evidence_facts TO :"runtime";
REVOKE ALL ON
    :"schema".cao_sample_repositories, :"schema".cao_sample_campaigns, :"schema".cao_sample_workflows,
    :"schema".cao_sample_runs, :"schema".cao_sample_audits, :"schema".cao_sample_domains,
    :"schema".cao_sample_tools, :"schema".cao_sample_skills, :"schema".cao_sample_friction,
    :"schema".cao_sample_issues, :"schema".cao_sample_experiments, :"schema".cao_sample_experiment_assignments,
    :"schema".cao_sample_graders, :"schema".cao_sample_grader_observations,
    :"schema".cao_sample_evals, :"schema".cao_sample_eval_observations,
    :"schema".cao_sample_statistical_frame, :"schema".cao_sample_statistical_membership,
    :"schema".cao_sample_statistical_strata, :"schema".cao_sample_cao_quality
    FROM PUBLIC, :"runtime";
GRANT SELECT, INSERT, DELETE ON
    :"schema".cao_sample_repositories, :"schema".cao_sample_campaigns, :"schema".cao_sample_workflows,
    :"schema".cao_sample_runs, :"schema".cao_sample_audits, :"schema".cao_sample_domains,
    :"schema".cao_sample_tools, :"schema".cao_sample_skills, :"schema".cao_sample_friction,
    :"schema".cao_sample_issues, :"schema".cao_sample_experiments, :"schema".cao_sample_experiment_assignments,
    :"schema".cao_sample_graders, :"schema".cao_sample_grader_observations,
    :"schema".cao_sample_evals, :"schema".cao_sample_eval_observations,
    :"schema".cao_sample_statistical_frame, :"schema".cao_sample_statistical_membership,
    :"schema".cao_sample_statistical_strata, :"schema".cao_sample_cao_quality TO :"runtime";
REVOKE ALL ON FUNCTION :"schema".cao_analyze_native() FROM PUBLIC, :"runtime";
GRANT EXECUTE ON FUNCTION :"schema".cao_analyze_native() TO :"runtime";
GRANT SELECT, INSERT, UPDATE, DELETE ON
    :"schema".cao_github_resources, :"schema".cao_quality, :"schema".cao_repository_lifecycle,
    :"schema".repositories, :"schema".campaigns, :"schema".workflows,
    :"schema".runs, :"schema".audits, :"schema".domains, :"schema".evals,
    :"schema".experiments, :"schema".eval_observations, :"schema".experiment_assignments,
    :"schema".friction, :"schema".graders, :"schema".grader_observations,
    :"schema".issues, :"schema".operational_values, :"schema".workflow_value_collections, :"schema".outcome_performance,
    :"schema".security_findings, :"schema".skills, :"schema".tools,
    :"schema".transactions, :"schema".work_items TO :"runtime";
-- The database administrator must separately grant TEMPORARY on this database
-- to runtime for ingestion staging. A schema owner cannot grant database rights.
COMMIT;
