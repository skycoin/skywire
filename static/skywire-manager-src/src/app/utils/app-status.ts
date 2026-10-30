/** How an app status reads: grey when stopped, red only when it failed. */
export type AppTone = 'running' | 'failed' | 'starting' | 'stopped';

/** The tone for an Application status code (0 stopped, 1 running, 2 failed, 3 starting). */
export function appStatusTone(status: number): AppTone {
  switch (status) {
    case 1: return 'running';
    case 2: return 'failed';
    case 3: return 'starting';
    default: return 'stopped';
  }
}

const dotClasses: Record<AppTone, string> = {
  running: 'dot-green',
  failed: 'dot-red',
  starting: 'dot-yellow',
  stopped: 'dot-grey',
};

/** The status dot class for an Application status code. */
export function appStatusDot(status: number): string {
  return dotClasses[appStatusTone(status)];
}

/** An app port for display: services with no port show a dash, not 0. */
export function portLabel(port: number | null | undefined): string {
  return port ? String(port) : '—';
}
