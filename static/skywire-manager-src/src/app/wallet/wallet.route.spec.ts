import { EnvironmentInjector, Provider, createEnvironmentInjector } from '@angular/core';
import { TestBed } from '@angular/core/testing';

import { NODE_CSRF_GUARDS_ACCESS } from '../../skycoin-wallet/app/node-access';
import { walletRoute } from './wallet.route';

describe('walletRoute', () => {
  it('tells the wallet the node CSRF warning does not apply behind the hypervisor', () => {
    const parent = TestBed.inject(EnvironmentInjector);
    const route = createEnvironmentInjector(walletRoute.providers as Provider[], parent);
    expect(route.get(NODE_CSRF_GUARDS_ACCESS)).toBeFalse();
    expect(parent.get(NODE_CSRF_GUARDS_ACCESS)).toBeTrue();
  });
});
