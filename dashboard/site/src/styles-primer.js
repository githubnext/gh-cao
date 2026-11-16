import { baseStyles } from './styles-base.js';
import { shellStyles } from './styles-shell.js';
import { viewStyles } from './styles-views.js';
import { chartStyles } from './styles-charts.js';
import { componentStyles } from './styles-components.js';
import { inboxStyles } from './styles-inbox.js';
import { overviewStyles } from './styles-overview.js';
import { operationStyles } from './styles-operations.js';
import { contentStyles } from './styles-content.js';
import { governanceStyles } from './styles-governance.js';
import { promptWizardStyles } from './styles-prompt-wizard.js';
import { workflowValueStyles } from './styles-workflow-value.js';
import { responsiveStyles } from './styles-responsive.js';
import { accessibilityStyles } from './styles-accessibility.js';

// Preserve cascade order, including the final responsive and accessibility overrides.
export const primerStyles = [
  baseStyles,
  shellStyles,
  viewStyles,
  chartStyles,
  componentStyles,
  inboxStyles,
  overviewStyles,
  operationStyles,
  contentStyles,
  governanceStyles,
  promptWizardStyles,
  workflowValueStyles,
  responsiveStyles,
  accessibilityStyles
].join('');

// Built sites inline only the styles needed to paint the shell and first page.
export const eagerPrimerStyles = [
  baseStyles,
  shellStyles,
  viewStyles,
  componentStyles,
  inboxStyles,
  overviewStyles,
  contentStyles,
  responsiveStyles,
  accessibilityStyles
].join('');

// Data-rendered charts, tables, and feature views load after startup.
export const deferredFeatureStyles = [
  chartStyles,
  operationStyles,
  governanceStyles,
  promptWizardStyles,
  workflowValueStyles
].join('');

// The overrides are repeated so they still follow the deferred rules they target.
export const deferredPrimerStyles = [
  deferredFeatureStyles,
  responsiveStyles,
  accessibilityStyles
].join('');
