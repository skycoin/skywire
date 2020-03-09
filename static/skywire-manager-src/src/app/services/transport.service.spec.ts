import { TestBed, inject } from '@angular/core/testing';

import { TransportService } from './transport.service';

describe('TransportService', () => {
  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [TransportService]
    });
  });

  it('should be created', inject([TransportService], (service: TransportService) => {
    expect(service).toBeTruthy();
  }));
});
