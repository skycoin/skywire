FROM multiarch/ubuntu-debootstrap:armhf-bionic

RUN wget --no-check-certificate https://dl.google.com/go/go1.14.4.linux-armv6l.tar.gz
RUN tar -C /usr/local -xzf go1.14.4.linux-armv6l.tar.gz
RUN export PATH=$PATH:/usr/local/go/bin

COPY . skywire

WORKDIR skywire

#ENTRYPOINT [ "/bin/sh" ]
ENTRYPOINT [ "/usr/local/go/bin/go", "test", "./..." ]
