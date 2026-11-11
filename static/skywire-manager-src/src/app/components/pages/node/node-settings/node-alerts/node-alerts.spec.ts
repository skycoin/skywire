import { formFor, formError, settingsFor, splitList } from './node-alerts.component';

describe('node alerts form', () => {
  const status = {
    settings: {
      email_to: ['a@example.com', 'b@example.com'],
      smtp: { host: 'smtp.example.com', port: 587, username: 'u', starttls: true, password_set: true },
      watch: ['02aa'],
      disk_warn_percent: 80,
    },
    enabled: true,
    offline_after: '15m0s',
    watched: [],
  };

  it('round-trips a status into settings, keeping the password and watch list', () => {
    const f = formFor(status);
    expect(f.emails).toBe('a@example.com, b@example.com');
    expect(f.delay).toBe('15m0s');
    expect(f.useRelay).toBeTrue();
    expect(f.password).toBe('');
    const s = settingsFor(f, status.settings);
    expect(s.email_to).toEqual(['a@example.com', 'b@example.com']);
    expect(s.offline_after).toBe('15m0s');
    expect(s.smtp!.port).toBe(587);
    expect(s.smtp!.password).toBe('');
    expect(s.watch).toEqual(['02aa']);
    expect(s.disk_warn_percent).toBe(80);
  });

  it('drops the relay when unticked', () => {
    const f = formFor(status);
    f.useRelay = false;
    expect(settingsFor(f, status.settings).smtp).toBeNull();
  });

  it('flags bad input', () => {
    const f = formFor(status);
    expect(formError(f)).toBe('');
    expect(formError({ ...f, emails: 'nope' })).toContain('not an email');
    expect(formError({ ...f, webhook: 'ftp://x' })).toContain('webhook');
    expect(formError({ ...f, host: '' })).toContain('host');
    expect(formError({ ...f, port: '70000' })).toContain('port');
    expect(formError({ ...f, emails: '' })).toBe('');
  });

  it('splits lists on commas and spaces', () => {
    expect(splitList(' a@x.io, b@x.io  c@x.io,')).toEqual(['a@x.io', 'b@x.io', 'c@x.io']);
  });
});
