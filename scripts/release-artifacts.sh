#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$repo_root"

usage() {
	cat <<'USAGE'
Usage:
  scripts/release-artifacts.sh build --version X.Y.Z [--dist-dir dist]
  scripts/release-artifacts.sh verify-manifest [--dist-dir dist]
  scripts/release-artifacts.sh verify-linux-static --version X.Y.Z [--dist-dir dist]
  scripts/release-artifacts.sh smoke-test --version X.Y.Z --goos <os> --goarch <arch> --archive-ext <ext> --binary-ext <ext> [--dist-dir dist]
USAGE
}

resolve_path() {
	local path="$1"
	case "$path" in
	/*) printf '%s\n' "$path" ;;
	*) printf '%s/%s\n' "$repo_root" "$path" ;;
	esac
}

require_value() {
	local name="$1"
	local value="$2"
	if [ -n "$value" ]; then
		return
	fi
	echo "Missing required argument: ${name}" >&2
	usage >&2
	exit 1
}

clean_dist_release_artifacts() {
	local dist_path="$1"
	find "$dist_path" -maxdepth 1 -type f \( -name 'builder_*.tar.gz' -o -name 'builder_*.zip' -o -name 'checksums.txt' \) -delete
}

release_targets() {
	cat <<'EOF'
darwin arm64
linux amd64
linux arm64
windows amd64
windows arm64
EOF
}

build_archives() {
	require_value "--version" "$version"

	local dist_path
	dist_path="$(resolve_path "$dist_dir")"
	mkdir -p "$dist_path"
	clean_dist_release_artifacts "$dist_path"

	local staging_dir
	staging_dir="$(mktemp -d)"

	local build_os build_arch ext archive_ext out
	while read -r build_os build_arch; do
		if [ "$build_os" = "windows" ]; then
			ext=".exe"
			archive_ext="zip"
		else
			ext=""
			archive_ext="tar.gz"
		fi

		out="builder_${version}_${build_os}_${build_arch}"
		env GOOS="$build_os" GOARCH="$build_arch" BUILDER_VERSION="$version" \
			bash scripts/build.sh --output "$staging_dir/${out}${ext}"

		if [ "$archive_ext" = "zip" ]; then
			(
				cd "$staging_dir"
				zip -q "$dist_path/${out}.zip" "${out}${ext}"
			)
		else
			(
				cd "$staging_dir"
				tar -czf "$dist_path/${out}.tar.gz" "${out}${ext}"
			)
		fi
	done < <(release_targets)

	(
		cd "$dist_path"
		shasum -a 256 "builder_${version}_"*.tar.gz "builder_${version}_"*.zip >checksums.txt
	)
}

verify_manifest() {
	local dist_path
	dist_path="$(resolve_path "$dist_dir")"
	(
		cd "$dist_path"
		shasum -a 256 -c checksums.txt
	)
}

verify_linux_static() {
	require_value "--version" "$version"

	local dist_path
	dist_path="$(resolve_path "$dist_dir")"

	local staging_dir
	staging_dir="$(mktemp -d)"

	local build_arch archive_path binary_path file_output
	for build_arch in amd64 arm64; do
		archive_path="$dist_path/builder_${version}_linux_${build_arch}.tar.gz"
		tar -xzf "$archive_path" -C "$staging_dir"

		binary_path="$staging_dir/builder_${version}_linux_${build_arch}"
		file_output="$(file "$binary_path")"
		echo "$file_output"

		case "$file_output" in
		*"dynamically linked"*)
			echo "Dynamic linking is not allowed for Linux release binaries." >&2
			exit 1
			;;
		*"statically linked"*) ;;
		*)
			echo "Unable to confirm static linking for ${binary_path}." >&2
			exit 1
			;;
		esac
	done
}

smoke_test() {
	require_value "--version" "$version"
	require_value "--goos" "$goos"
	require_value "--goarch" "$goarch"
	require_value "--archive-ext" "$archive_ext"

	local dist_path
	dist_path="$(resolve_path "$dist_dir")"

	local asset_base archive_path smoke_dir binary_path
	asset_base="builder_${version}_${goos}_${goarch}"
	archive_path="$dist_path/${asset_base}.${archive_ext}"
	smoke_dir="$(mktemp -d)"

	case "$archive_ext" in
	tar.gz)
		tar -xzf "$archive_path" -C "$smoke_dir"
		;;
	zip)
		unzip -q "$archive_path" -d "$smoke_dir"
		;;
	*)
		echo "Unsupported archive extension: ${archive_ext}" >&2
		exit 1
		;;
	esac

	binary_path="$smoke_dir/${asset_base}${binary_ext}"
	local version_output expected_version
	version_output="$("$binary_path" --version)"
	expected_version="${version#v}"
	if [ "$version_output" != "$expected_version" ]; then
		echo "unexpected version output for ${binary_path}: got ${version_output}, want ${expected_version}" >&2
		exit 1
	fi
	"$binary_path" --help >/dev/null
}

mode="${1:-}"
if [ -z "$mode" ]; then
	usage >&2
	exit 1
fi
shift

dist_dir="dist"
version=""
goos=""
goarch=""
archive_ext=""
binary_ext=""

while [[ $# -gt 0 ]]; do
	case "$1" in
	--dist-dir)
		dist_dir="${2:-}"
		shift 2
		;;
	--version)
		version="${2:-}"
		shift 2
		;;
	--goos)
		goos="${2:-}"
		shift 2
		;;
	--goarch)
		goarch="${2:-}"
		shift 2
		;;
	--archive-ext)
		archive_ext="${2:-}"
		shift 2
		;;
	--binary-ext)
		binary_ext="${2:-}"
		shift 2
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		echo "Unknown argument: $1" >&2
		usage >&2
		exit 1
		;;
	esac
done

version="${version#v}"

case "$mode" in
build)
	build_archives
	;;
verify-manifest)
	verify_manifest
	;;
verify-linux-static)
	verify_linux_static
	;;
smoke-test)
	smoke_test
	;;
*)
	echo "Unknown mode: $mode" >&2
	usage >&2
	exit 1
	;;
esac
