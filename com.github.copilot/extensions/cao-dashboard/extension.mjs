import { joinSession } from "@github/copilot-sdk/extension";
import { createDashboardExtension } from "./dashboard-extension.mjs";

const { config } = createDashboardExtension();
await joinSession(config);
