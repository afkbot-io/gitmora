FROM scratch
COPY webui-http.test /webui-http.test
USER 65532:65532
ENTRYPOINT ["/webui-http.test"]
