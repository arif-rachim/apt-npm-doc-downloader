# Thin wrapper around build.sh, which is the real build and needs only bash.
.PHONY: all build test check offline dist release clean
all: build
build test check offline dist release clean:
	@./build.sh $@
