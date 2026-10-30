import { Pipe, PipeTransform } from '@angular/core';

/**
 * A visor version without the commit repeated after git describe, so
 * v1.3.103-175-gf0faaa145-f0faaa1453c5 reads v1.3.103-175-gf0faaa145.
 */
export function versionLabel(version: string | null | undefined): string {
  const v = version || '';
  const m = /^(.*-g([0-9a-f]{4,}))-([0-9a-f]{4,})$/.exec(v);
  if (m && (m[3].startsWith(m[2]) || m[2].startsWith(m[3]))) {
    return m[1];
  }

  return v;
}

@Pipe({
  name: 'versionLabel',
  standalone: false,
})
export class VersionLabelPipe implements PipeTransform {
  transform(version: string | null | undefined): string {
    return versionLabel(version);
  }
}
