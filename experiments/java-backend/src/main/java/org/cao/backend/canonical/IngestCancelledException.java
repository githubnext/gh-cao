package org.cao.backend.canonical;

/** Ingestion was cancelled before publication; the previously active dataset keeps serving. */
public final class IngestCancelledException extends RuntimeException {
    private static final long serialVersionUID = 1L;

    public IngestCancelledException() {
        super("ingestion cancelled before publication");
    }
}
