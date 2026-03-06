#!/usr/bin/bash
## CHANGELOG GENERATOR SCRIPT
# Usage: ./scripts/changelog.sh <PR#lowest> <PR#highest>
# Example: ./scripts/changelog.sh 2158 2188
#
# Generates changelog entries for merged PRs in the given range using GitHub CLI.
# Requires: gh (GitHub CLI) installed and authenticated

[[ $1 == "" ]] || [[ $2 == "" ]] && {
    echo "Usage: $0 <PR#lowest> <PR#highest>"
    echo "Example: $0 2158 2188"
    exit 1
}

REPO="skycoin/skywire"
LOW=$1
HIGH=$2

for pr_num in $(seq "$HIGH" -1 "$LOW"); do
    # Get PR info using gh CLI
    result=$(gh pr view "$pr_num" --repo "$REPO" --json state,title 2>/dev/null)

    if [[ $? -eq 0 ]]; then
        state=$(echo "$result" | jq -r '.state')
        title=$(echo "$result" | jq -r '.title')

        if [[ "$state" == "MERGED" ]]; then
            echo "-   ${title}  [#${pr_num}](https://github.com/${REPO}/pull/${pr_num})"
        fi
    fi
done
