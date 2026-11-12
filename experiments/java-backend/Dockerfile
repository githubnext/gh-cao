# Local build: podman build -t cao-java-backend experiments/java-backend (after `mvn verify`).
# Provider-neutral; contains no data or credentials. Not built or published by this repository.
FROM eclipse-temurin:21-jre
RUN useradd --system --uid 10001 --no-create-home cao
COPY target/cao-java-backend-0.1.0-SNAPSHOT.jar /app/cao-java-backend.jar
COPY target/lib /app/lib
USER 10001
ENV CAO_BIND=0.0.0.0 CAO_ALLOW_NON_LOOPBACK=true CAO_PORT=8080
EXPOSE 8080
ENTRYPOINT ["java", "-cp", "/app/cao-java-backend.jar:/app/lib/*", "org.cao.backend.http.Main"]
