FROM scratch

LABEL maintainer="Miguel Redmars <miguel.redmars@gmail.com>"

ADD bin/wemo-homecontrol /

VOLUME ["/config"]

VOLUME [ "/data" ]

ENV HOME /data

CMD ["/wemo-homecontrol"]