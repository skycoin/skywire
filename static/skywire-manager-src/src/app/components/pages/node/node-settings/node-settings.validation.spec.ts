import { SettingRow, settingError } from './node-settings.component';

// The form refuses what the visor would refuse on save (autoconfigcmd.checkValue),
// so a typo shows up beside the field instead of at the next autoconfig run.
describe('settings validation', () => {
  const pk = '02' + 'a'.repeat(64);
  const row = (format: string, check: string, secret = false): SettingRow => {
    return {
      key: 'K', flag: 'k', negate: false, format: format, help: '', default: '', note: '',
      set: false, value: '', secret: secret, check: check,
    };
  };

  it('accepts empty values and the secret placeholder', () => {
    expect(settingError(row('string', 'pk'), '')).toBe('');
    expect(settingError(row('string', 'pk', true), '(set)')).toBe('');
  });

  it('checks whole numbers', () => {
    expect(settingError(row('int', ''), '-3')).toBe('');
    expect(settingError(row('int', ''), '3.5')).not.toBe('');
  });

  it('checks keys and key lists', () => {
    expect(settingError(row('string', 'pk'), pk)).toBe('');
    expect(settingError(row('string', 'pk'), '02abc')).not.toBe('');
    expect(settingError(row('bashArray', 'pks'), pk + ', ' + pk)).toBe('');
    expect(settingError(row('bashArray', 'pks'), pk + ',nope')).not.toBe('');
  });

  it('checks host:port, durations and log levels', () => {
    expect(settingError(row('string', 'hostport'), ':8000')).toBe('');
    expect(settingError(row('string', 'hostport'), '[::1]:80')).toBe('');
    expect(settingError(row('string', 'hostport'), '127.0.0.1')).not.toBe('');
    expect(settingError(row('string', 'hostport'), 'h:70000')).not.toBe('');
    expect(settingError(row('string', 'duration'), '1h30m')).toBe('');
    expect(settingError(row('string', 'duration'), '10')).not.toBe('');
    expect(settingError(row('string', 'loglevel'), 'Debug')).toBe('');
    expect(settingError(row('string', 'loglevel'), 'loud')).not.toBe('');
  });
});
