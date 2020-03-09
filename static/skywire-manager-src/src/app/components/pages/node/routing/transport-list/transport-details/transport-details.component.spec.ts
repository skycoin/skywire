import { async, ComponentFixture, TestBed } from '@angular/core/testing';

import { TransportDetailsComponent } from './transport-details.component';

describe('TransportDetailsComponent', () => {
  let component: TransportDetailsComponent;
  let fixture: ComponentFixture<TransportDetailsComponent>;

  beforeEach(async(() => {
    TestBed.configureTestingModule({
      declarations: [ TransportDetailsComponent ]
    })
    .compileComponents();
  }));

  beforeEach(() => {
    fixture = TestBed.createComponent(TransportDetailsComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});
