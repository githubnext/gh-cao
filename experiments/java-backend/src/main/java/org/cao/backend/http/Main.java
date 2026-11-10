package org.cao.backend.http;

/** Entry point: reads listener settings from the environment and serves until terminated. */
public final class Main {
    private Main() { }

    public static void main(String[] args) throws Exception {
        ServerConfig config = ServerConfig.fromEnvironment(System.getenv());
        ApiServer server = new ApiServer(config, ServiceState.unconfigured());
        Runtime.getRuntime().addShutdownHook(
            new Thread(() -> server.stop(config.shutdownGraceSeconds())));
        server.start();
        System.out.println("listening on " + config.bind() + ":" + server.port());
    }
}
