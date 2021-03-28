FROM ubuntu:18.04

MAINTAINER Bartek Nowotarski <bartek@stellar.org>

# to remove tzdata interactive flow
ENV DEBIAN_FRONTEND=noninteractive

ADD dependencies /
RUN ["chmod", "+x", "dependencies"]
RUN /dependencies

ADD captive-core-pubnet.cfg /

ADD start /
RUN ["chmod", "+x", "start"]

ENTRYPOINT ["/start"]