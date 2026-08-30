FROM scratch

WORKDIR /app
COPY demo-service /app/demo-service

USER 65532:65532
ENTRYPOINT ["/app/demo-service"]
CMD ["-config", "/app/config/config.json"]
