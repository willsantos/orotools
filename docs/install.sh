#!/bin/sh
# install.sh — instala o oro a partir da última release do GitHub.
#
# Uso:
#   curl -fsSL https://willsantos.github.io/orotools/install.sh | bash
#   install.sh --version vX.Y.Z   (instala uma versão específica)
#
# Requisitos: DIST-06, DIST-07, NFR-2 (POSIX sh — compatível com dash;
# dependências: curl, sha256sum ou shasum, tar).

set -eu

REPO="willsantos/orotools"
# O override é um gancho de teste (sandbox local); produção usa o GitHub.
RELEASES_URL="${ORO_RELEASES_URL:-https://github.com/${REPO}/releases}"
LATEST_URL="${RELEASES_URL}/latest"

say() { printf '%s\n' "$*"; }
warn() { printf '%s\n' "aviso: $*" >&2; }
die() { printf '%s\n' "install.sh: $*" >&2; exit 1; }

# ---------------------------------------------------------------- flags

VERSION=""
while [ $# -gt 0 ]; do
	case "$1" in
	--version)
		[ $# -ge 2 ] || die "--version exige um valor (ex.: --version v0.5.0)"
		VERSION="$2"
		shift 2
		;;
	--version=*)
		VERSION="${1#*=}"
		shift
		;;
	-h|--help)
		say "uso: install.sh [--version vX.Y.Z]"
		exit 0
		;;
	*)
		die "argumento desconhecido: $1 (veja --help)"
		;;
	esac
done

# ---------------------------------------------------------------- dependências

command -v curl >/dev/null 2>&1 || die "curl não encontrado no PATH."
command -v tar >/dev/null 2>&1 || die "tar não encontrado no PATH."

# sha256sum (GNU coreutils) com fallback para shasum (macOS).
checksum_cmd() {
	if command -v sha256sum >/dev/null 2>&1; then
		printf 'sha256sum'
	elif command -v shasum >/dev/null 2>&1; then
		printf 'shasum -a 256'
	else
		return 1
	fi
}

# ---------------------------------------------------------------- SO / arch

case "$(uname -s)" in
	Linux) OS="linux" ;;
	Darwin) OS="darwin" ;;
	*)
		die "SO não suportado ($(uname -s)). Baixe o asset manualmente em ${RELEASES_URL}."
		;;
esac

ARCH_CASE="$(uname -m)"
case "$ARCH_CASE" in
	x86_64|amd64) ARCH="amd64" ;;
	aarch64|arm64) ARCH="arm64" ;;
	*)
		die "arquitetura não suportada (${ARCH_CASE}). Assets disponíveis em ${RELEASES_URL}."
		;;
esac

# ---------------------------------------------------------------- última release

if [ -z "$VERSION" ]; then
	# A URL releases/latest redireciona para .../tag/vX.Y.Z — sem API, sem rate limit.
	TAG_URL="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$LATEST_URL")" \
		|| die "não foi possível resolver a última release. Verifique sua conexão ou baixe manualmente em ${RELEASES_URL}."
	VERSION="$(basename "$TAG_URL")"
	echo "$VERSION" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' \
		|| die "última tag inválida (${VERSION}). Consulte ${RELEASES_URL}."
fi
case "$VERSION" in
	v*) ;;
	*) VERSION="v${VERSION}" ;;
esac

ASSET="oro_${VERSION#v}_${OS}_${ARCH}.tar.gz"
ASSET_URL="${RELEASES_URL}/download/${VERSION}/${ASSET}"
CHECKSUMS_URL="${RELEASES_URL}/download/${VERSION}/checksums.txt"

# ---------------------------------------------------------------- download + verificação

TMPDIR_INSTALL="$(mktemp -d)"
trap 'rm -rf "$TMPDIR_INSTALL"' EXIT INT TERM

say "baixando ${ASSET}..."
curl -fsSL --retry 3 -o "${TMPDIR_INSTALL}/${ASSET}" "$ASSET_URL" \
	|| die "falha no download de ${ASSET_URL}."

curl -fsSL --retry 3 -o "${TMPDIR_INSTALL}/checksums.txt" "$CHECKSUMS_URL" \
	|| die "falha no download de checksums.txt."

CK="$(checksum_cmd)" || die "sha256sum ou shasum é necessário para validar o checksum."
EXPECTED="$(awk -v asset="$ASSET" '$2 == asset { print $1 }' "${TMPDIR_INSTALL}/checksums.txt")"
[ -n "$EXPECTED" ] || die "asset ${ASSET} não listado em checksums.txt."
ACTUAL="$($CK "${TMPDIR_INSTALL}/${ASSET}" | awk '{ print $1 }')"

if [ "$ACTUAL" != "$EXPECTED" ]; then
	die "checksum inválido (esperado ${EXPECTED}, obtido ${ACTUAL}). Nada foi instalado."
fi

say "checksum ok."

# ---------------------------------------------------------------- instalação

if [ "$(id -u)" -eq 0 ]; then
	BINDIR="/usr/local/bin"
else
	BINDIR="${HOME}/.local/bin"
	mkdir -p "$BINDIR" || die "não foi possível criar ${BINDIR}."
fi

# O arquivo .tmp garante que um install interrompido não deixa um binário
# pela metade no destino.
tar -xzf "${TMPDIR_INSTALL}/${ASSET}" -C "$TMPDIR_INSTALL" oro \
	|| die "falha ao extrair ${ASSET}."
mv "${TMPDIR_INSTALL}/oro" "${BINDIR}/oro.tmp" && mv "${BINDIR}/oro.tmp" "${BINDIR}/oro" \
	|| die "falha ao instalar em ${BINDIR} (sem permissão? use sudo ou ajuste o destino)."
chmod 0755 "${BINDIR}/oro"

# ---------------------------------------------------------------- PATH

case ":${PATH}:" in
	*":${BINDIR}:"*) ;;
	*)
		warn "${BINDIR} não está no seu PATH."
		case "$(basename "${SHELL:-}")" in
			zsh) RC="${HOME}/.zshrc" ;;
			bash) RC="${HOME}/.bashrc" ;;
			*) RC="${HOME}/.profile" ;;
		esac
		say "adicione ao ${RC}:"
		say "  export PATH=\"${BINDIR}:\$PATH\""
		;;
esac

say "oro ${VERSION} instalado em ${BINDIR}/oro — rode 'oro --version' para conferir."
