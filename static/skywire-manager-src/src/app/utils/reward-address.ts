import { AbstractControl, ValidationErrors, ValidatorFn } from '@angular/forms';

/**
 * Accepts an account xpub, an empty field, or a single address when acceptRisk
 * returns true. The visor still refuses a single address that has sent coins.
 */
export function rewardAddressValidator(acceptRisk: () => boolean): ValidatorFn {
  return (c: AbstractControl): ValidationErrors | null => {
    const v = ((c.value as string) || '').trim();
    if (!v || v.startsWith('xpub') || acceptRisk()) {
      return null;
    }

    return { xpubRequired: true };
  };
}

/** Whether a save of address needs the single address opt-in sent. */
export function needsAcceptRisk(address: string, acceptRisk: boolean): boolean {
  const v = (address || '').trim();

  return acceptRisk && !!v && !v.startsWith('xpub');
}
