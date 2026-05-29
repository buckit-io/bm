FROM rockylinux:9

RUN dnf install -y \
      iputils \
      iproute \
      openssh-server \
      passwd \
      procps-ng \
      python3 \
      sudo \
      systemd \
      util-linux \
      xfsprogs \
  && dnf clean all \
  && rm -rf /var/cache/dnf

RUN mkdir -p /var/run/sshd /var/lib/bm-target-drives /data /etc/systemd/system/multi-user.target.wants \
  && ln -sf /usr/lib/systemd/system/sshd.service /etc/systemd/system/multi-user.target.wants/sshd.service \
  && sed -i 's/^#\\?\\s*PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config \
  && sed -i 's/^#\\?\\s*PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config \
  && sed -i 's/^#\\?\\s*UsePAM.*/UsePAM yes/' /etc/ssh/sshd_config \
  && grep -q '^PermitRootLogin ' /etc/ssh/sshd_config || echo 'PermitRootLogin yes' >> /etc/ssh/sshd_config \
  && grep -q '^PasswordAuthentication ' /etc/ssh/sshd_config || echo 'PasswordAuthentication yes' >> /etc/ssh/sshd_config \
  && grep -q '^UsePAM ' /etc/ssh/sshd_config || echo 'UsePAM yes' >> /etc/ssh/sshd_config

COPY integration-test/targets/entrypoint.sh /usr/local/bin/bm-e2e-target-entrypoint.sh
COPY integration-test/targets/healthcheck.sh /usr/local/bin/bm-e2e-target-healthcheck.sh
RUN chmod +x /usr/local/bin/bm-e2e-target-entrypoint.sh /usr/local/bin/bm-e2e-target-healthcheck.sh

STOPSIGNAL SIGRTMIN+3
HEALTHCHECK --interval=5s --timeout=5s --start-period=15s --retries=30 CMD ["/usr/local/bin/bm-e2e-target-healthcheck.sh"]

ENTRYPOINT ["/usr/local/bin/bm-e2e-target-entrypoint.sh"]
