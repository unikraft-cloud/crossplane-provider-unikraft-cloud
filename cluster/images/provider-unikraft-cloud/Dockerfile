FROM gcr.io/distroless/static@sha256:49af06135e8bbe8ddc46c1d28b0bd00961aae9c9ed090bbc0237f58e1462dd4b

ARG TARGETOS
ARG TARGETARCH

ADD bin/$TARGETOS\_$TARGETARCH/provider /usr/local/bin/crossplane-unikraft-cloud-provider

USER 65532
ENTRYPOINT ["crossplane-unikraft-cloud-provider"]
