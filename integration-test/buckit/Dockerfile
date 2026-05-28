FROM rockylinux:9

ARG BUCKIT_RPM_FILENAME

WORKDIR /srv

COPY .generated/buckit/${BUCKIT_RPM_FILENAME} /tmp/buckit.rpm

RUN dnf install -y shadow-utils /tmp/buckit.rpm \
 && dnf clean all \
 && rm -rf /var/cache/dnf /tmp/buckit.rpm

EXPOSE 9000 9001

HEALTHCHECK --interval=5s --timeout=3s --start-period=10s --retries=20 \
  CMD curl -fsS http://127.0.0.1:9000/minio/health/live || exit 1

ENTRYPOINT ["/usr/local/bin/buckit"]
