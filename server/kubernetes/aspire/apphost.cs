#:sdk Aspire.AppHost.Sdk@13.6.5
#:package CommunityToolkit.Aspire.Hosting.Kind@13.6.1-preview.1.261026-0455

using System.Text.RegularExpressions;

// Optional: runs the server/kubernetes/local.sh evaluation on a standalone kind
// cluster instead of Docker Desktop Kubernetes. See ../README.md.
var clusterName = Environment.GetEnvironmentVariable("CAO_LOCAL_KIND_CLUSTER") is { Length: > 0 } configured
    ? configured
    : "cao-local";
if (!Regex.IsMatch(clusterName, "^[a-z0-9]([-a-z0-9]{0,30}[a-z0-9])?$"))
{
    throw new InvalidOperationException("Invalid CAO_LOCAL_KIND_CLUSTER name.");
}

var builder = DistributedApplication.CreateBuilder(args);
var root = Path.GetFullPath(Path.Combine(builder.AppHostDirectory, "..", "..", ".."));

// Persistent so PostgreSQL data survives AppHost restarts. Delete it yourself with
// `kind delete cluster --name cao-local` when you are done.
var cluster = builder.AddKindCluster(clusterName)
    .WithClusterLifetime(ClusterLifetime.Persistent);

IResourceBuilder<ExecutableResource> Step(string command) =>
    builder.AddExecutable(command, "bash", root, "server/kubernetes/local.sh", command)
        .WithReference(cluster)
        .WaitFor(cluster)
        .WithEnvironment("CAO_LOCAL_KIND_CLUSTER", clusterName)
        .WithOtlpExporter();

// `prepare` refuses to overwrite private state, so only run it the first time.
var build = Step("build");
if (!Directory.Exists(Path.Combine(root, ".tmp", "cao-kubernetes-local")))
{
    build.WaitForCompletion(Step("prepare"));
}

Step("database").WaitForCompletion(build);

builder.Build().Run();
