FROM rockylinux:9

RUN dnf install -y \
      curl-minimal \
      openssh-server \
      passwd \
      procps-ng \
      shadow-utils \
      systemd \
      util-linux \
      xfsprogs \
  && dnf clean all \
  && rm -rf /var/cache/dnf

ARG BUCKIT_RPM_FILENAME

COPY .generated/buckit-systemd/${BUCKIT_RPM_FILENAME} /tmp/buckit.rpm

RUN dnf install -y /tmp/buckit.rpm \
  && rm -f /tmp/buckit.rpm

RUN mkdir -p /var/lib/buckit-drives /data /var/run/sshd /etc/systemd/system/multi-user.target.wants \
  && sed -i 's/^#\\?\\s*PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config \
  && sed -i 's/^#\\?\\s*PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config \
  && sed -i 's/^#\\?\\s*UsePAM.*/UsePAM yes/' /etc/ssh/sshd_config \
  && grep -q '^PermitRootLogin ' /etc/ssh/sshd_config || echo 'PermitRootLogin yes' >> /etc/ssh/sshd_config \
  && grep -q '^PasswordAuthentication ' /etc/ssh/sshd_config || echo 'PasswordAuthentication yes' >> /etc/ssh/sshd_config \
  && grep -q '^UsePAM ' /etc/ssh/sshd_config || echo 'UsePAM yes' >> /etc/ssh/sshd_config \
  && ln -sf /usr/lib/systemd/system/sshd.service /etc/systemd/system/multi-user.target.wants/sshd.service \
  && ln -sf /usr/lib/systemd/system/buckit.service /etc/systemd/system/multi-user.target.wants/buckit.service

COPY buckit-systemd/entrypoint.sh /usr/local/bin/bm-e2e-buckit-systemd-entrypoint.sh
COPY buckit-systemd/healthcheck.sh /usr/local/bin/bm-e2e-buckit-systemd-healthcheck.sh

RUN chmod +x \
  /usr/local/bin/bm-e2e-buckit-systemd-entrypoint.sh \
  /usr/local/bin/bm-e2e-buckit-systemd-healthcheck.sh

EXPOSE 9000 9001 22

STOPSIGNAL SIGRTMIN+3
HEALTHCHECK --interval=5s --timeout=5s --start-period=20s --retries=40 CMD ["/usr/local/bin/bm-e2e-buckit-systemd-healthcheck.sh"]

ENTRYPOINT ["/usr/local/bin/bm-e2e-buckit-systemd-entrypoint.sh"]
