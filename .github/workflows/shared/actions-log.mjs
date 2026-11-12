function message(strings, values) {
  return strings.reduce((text, part, index) => (
    text + part + (index < values.length ? String(values[index]) : "")
  ), "");
}

function workflowCommand(command, text) {
  const escaped = text
    .replaceAll("%", "%25")
    .replaceAll("\r", "%0D")
    .replaceAll("\n", "%0A");
  console.log(`::${command}::${escaped}`);
}

export const actionsLog = {
  info(strings, ...values) {
    const text = message(strings, values);
    if (globalThis.core?.info) globalThis.core.info(text);
    else console.log(text);
  },
  debug(strings, ...values) {
    const text = message(strings, values);
    if (globalThis.core?.debug) globalThis.core.debug(text);
    else workflowCommand("debug", text);
  },
  notice(strings, ...values) {
    const text = message(strings, values);
    if (globalThis.core?.notice) globalThis.core.notice(text);
    else workflowCommand("notice", text);
  },
  warning(strings, ...values) {
    const text = message(strings, values);
    if (globalThis.core?.warning) globalThis.core.warning(text);
    else workflowCommand("warning", text);
  },
  error(strings, ...values) {
    const text = message(strings, values);
    if (globalThis.core?.error) globalThis.core.error(text);
    else workflowCommand("error", text);
  },
  group(strings, ...values) {
    const text = message(strings, values);
    if (globalThis.core?.startGroup) globalThis.core.startGroup(text);
    else workflowCommand("group", text);
  },
  endGroup() {
    if (globalThis.core?.endGroup) globalThis.core.endGroup();
    else console.log("::endgroup::");
  },
};
