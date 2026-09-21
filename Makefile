# Thin wrapper around build.sh, which is the real build and needs only bash.
.PHONY: all build test check offline windows dist clean
all: build
build test check offline windows dist clean:
	@./build.sh $@
