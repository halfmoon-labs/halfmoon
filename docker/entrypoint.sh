#!/bin/sh
set -e

# First-run: neither config nor workspace exists.
# If config.json is already mounted but workspace is missing we skip onboard to
# avoid the interactive "Overwrite? (y/n)" prompt hanging in a non-TTY container.
if [ ! -d "${HOME}/.halfmoon/workspace" ] && [ ! -f "${HOME}/.halfmoon/config.json" ]; then
    halfmoon onboard
    echo ""
    echo "First-run setup complete."
    echo "Edit ${HOME}/.halfmoon/config.json (add your API key, etc.) then restart the container."
    exit 0
fi

exec halfmoon gateway "$@"
