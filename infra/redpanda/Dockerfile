# The broker as Render runs it: the official image, plus the one step a
# Blueprint has nowhere else to put — creating the pipeline's topics.
#
# Compose does not use this image; it creates topics with its redpanda-init
# job instead. Both run the same create-topics.sh, so the topic list has one
# source.
#
# Pulled from Docker Hub rather than docker.redpanda.com: Render's fetcher
# intermittently failed on that host ("could not be fetched"), and the tag is
# identical on both.
# v24.2.27, not v24.2.7: on .7 the HTTP proxy spins the broker at 100% CPU,
# for good, the moment a consumer instance is deleted — which every worker
# does on shutdown, so one redeploy of any worker stopped the whole pipeline.
# Reproduced on .7 three times out of three and on .27 zero times out of three
# (same stack, same traffic). Keep docker-compose.yml on the same version.
ARG REDPANDA_VERSION=v24.2.27
FROM docker.io/redpandadata/redpanda:${REDPANDA_VERSION}

COPY create-topics.sh render-start.sh /opt/countersign/

ENTRYPOINT ["/bin/bash", "/opt/countersign/render-start.sh"]

# --overprovisioned tells Redpanda it is sharing a machine, which it must be
# told explicitly or it tunes itself for a dedicated box and refuses to start
# on a small instance. The advertised addresses are 127.0.0.1 because the only
# Kafka client is the HTTP proxy inside this same container; other services
# reach the proxy on 8082 by hostport and never use an advertised address.
#
# consumer_instance_timeout_ms is 60s instead of 5 minutes because workers no
# longer delete their consumer on shutdown (that hangs this broker; see
# services/pipeline/internal/bus Close). A dead instance is reaped by expiry, so
# this is how long a restarted worker can wait before it is assigned anything.
CMD ["--smp", "1", \
     "--memory", "1G", \
     "--reserve-memory", "0M", \
     "--overprovisioned", \
     "--kafka-addr", "0.0.0.0:9092", \
     "--advertise-kafka-addr", "127.0.0.1:9092", \
     "--pandaproxy-addr", "0.0.0.0:8082", \
     "--advertise-pandaproxy-addr", "127.0.0.1:8082",      "--set", "pandaproxy.consumer_instance_timeout_ms=60000"]
