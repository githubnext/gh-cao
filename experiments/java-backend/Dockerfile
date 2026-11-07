# Build context: experiments/java-backend/ after `mvn package` (target/ holds the jar and lib/).
# Provider-neutral and unexecuted in CI; no credentials or data are baked into the image.
FROM eclipse-temurin:21-jre-jammy
RUN groupadd --system --gid 10001 cao && useradd --system --uid 10001 --gid cao --no-create-home cao
WORKDIR /app
COPY target/cao-java-backend-0.1.0-SNAPSHOT.jar /app/cao-java-backend.jar
COPY target/lib /app/lib
USER 10001:10001
# The container must listen on all interfaces; a platform ingress/authenticating proxy must front it.
ENV CAO_HOST=0.0.0.0 CAO_PORT=8080
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s \
  CMD ["java", "-cp", "/app/cao-java-backend.jar", "org.cao.backend.HealthCheck"]
ENTRYPOINT ["java", "-XX:MaxRAMPercentage=75", "-jar", "/app/cao-java-backend.jar"]
