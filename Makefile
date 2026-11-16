STANDARDS_DIR ?= $(wildcard $(CURDIR)/../go-standards)
include standard.mk

# Repository-specific gates appended to `make check`.
CHECK_EXTRA := modverify delivery

.PHONY: modverify delivery

modverify:
	$(GO) mod verify

# Clean source, published connector dependencies and an independent consumer
# build, plus the checker's and the release tooling's own tests.
delivery:
	python3 scripts/check-delivery.py --modules-only
	python3 scripts/test_check_delivery.py -v
	python3 -O scripts/test_check_delivery.py -v
	python3 scripts/test_release.py -v
	python3 scripts/check-delivery.py

# Explicit private-corpus replay; never part of ordinary tests or CI.
.PHONY: corpus
corpus:
	$(GO) test -tags=corpus -run '^TestPrivateControllerCorpus$$' -count=1 -v ./wlan
