#!/usr/bin/env bash
# Full-screen caption card for the tutorial video: clears the screen and
# prints the arguments centered-ish. First arg is the headline (bold cyan),
# the rest are body lines (default color). Used by the parts/*.tape scenes.
clear
printf '\n\n\n\n\n\n\n\n\n\n'
indent='        '
printf '%s\033[1;36m%s\033[0m\n\n' "$indent" "$1"
shift
for line in "$@"; do
  printf '%s%s\n' "$indent" "$line"
done
