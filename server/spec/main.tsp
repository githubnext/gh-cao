import "@typespec/http";
import "@typespec/openapi3";
import "@typespec/openapi";
import "@typespec/json-schema";
import "./common.tsp";
import "./query.tsp";
import "./health.tsp";
import "./discovery.tsp";
import "./auth.tsp";
import "./canonical.tsp";
import "./github.tsp";
import "./memory.tsp";
import "./operations.tsp";
import "./value.tsp";
import "./mcp.tsp";
import "./admission.tsp";
import "./session-archive.tsp";

using TypeSpec.Http;
using TypeSpec.OpenAPI;

@service(#{ title: "CAO dashboard server" })
@info(#{ version: "2.12.0" })
@doc("The Go dashboard server HTTP contract. Local mode uses a loopback bearer capability; hosted mode uses an authorized GitHub OAuth session and X-CSRF-Token on protected mutations. The optional MCP HTTP transport is covered, but its protocol messages and tool schemas are owned by the MCP contract; dashboard static assets are outside this contract.")
namespace Cao;
