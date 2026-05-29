FROM rockylinux:9

RUN dnf install -y \
      curl-minimal \
      openssh-server \
      passwd \
      procps-ng \
      shadow-utils \
      systemd \
      util-linux \
      wget \
      xfsprogs \
  && dnf clean all \
  && rm -rf /var/cache/dnf

ARG TARGETARCH
RUN wget "https://dl.min.io/server/minio/release/linux-${TARGETARCH}/minio.rpm" -O /tmp/minio.rpm \
  && dnf install -y /tmp/minio.rpm \
  && rm -f /tmp/minio.rpm

RUN wget "https://dl.min.io/client/mc/release/linux-${TARGETARCH}/mc" -O /usr/local/bin/mc \
  && chmod +x /usr/local/bin/mc

RUN groupadd -r minio-user \
  && useradd -M -r -g minio-user minio-user

RUN mkdir -p /var/lib/minio-drives /mnt/data /var/run/sshd /etc/systemd/system/multi-user.target.wants \
  && sed -i 's/^#\\?PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config \
  && sed -i 's/^#\\?PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config \
  && sed -i 's/^#\\?UsePAM.*/UsePAM no/' /etc/ssh/sshd_config \
  && grep -q '^PermitRootLogin ' /etc/ssh/sshd_config || echo 'PermitRootLogin yes' >> /etc/ssh/sshd_config \
  && grep -q '^PasswordAuthentication ' /etc/ssh/sshd_config || echo 'PasswordAuthentication yes' >> /etc/ssh/sshd_config \
  && grep -q '^UsePAM ' /etc/ssh/sshd_config || echo 'UsePAM no' >> /etc/ssh/sshd_config \
  && ln -sf /usr/lib/systemd/system/minio.service /etc/systemd/system/multi-user.target.wants/minio.service \
  && ln -sf /usr/lib/systemd/system/sshd.service /etc/systemd/system/multi-user.target.wants/sshd.service

COPY integration-test/minio/entrypoint.sh /usr/local/bin/bm-e2e-minio-entrypoint.sh
COPY integration-test/minio/seed.sh /usr/local/bin/bm-e2e-minio-seed.sh
COPY integration-test/minio/healthcheck.sh /usr/local/bin/bm-e2e-minio-healthcheck.sh
RUN chmod +x \
  /usr/local/bin/bm-e2e-minio-entrypoint.sh \
  /usr/local/bin/bm-e2e-minio-seed.sh \
  /usr/local/bin/bm-e2e-minio-healthcheck.sh

RUN cat >/etc/systemd/system/minio-seed.service <<'EOF'
[Unit]
Description=Seed MinIO fixture data for bm e2e migration tests
After=minio.service network-online.target
Wants=minio.service

[Service]
Type=oneshot
EnvironmentFile=-/etc/default/bm-e2e-minio-seed
ExecStart=/usr/local/bin/bm-e2e-minio-seed.sh
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
EOF

RUN ln -sf /etc/systemd/system/minio-seed.service /etc/systemd/system/multi-user.target.wants/minio-seed.service

EXPOSE 9000 9001 22

STOPSIGNAL SIGRTMIN+3
HEALTHCHECK --interval=5s --timeout=5s --start-period=20s --retries=40 CMD ["/usr/local/bin/bm-e2e-minio-healthcheck.sh"]

ENTRYPOINT ["/usr/local/bin/bm-e2e-minio-entrypoint.sh"]
