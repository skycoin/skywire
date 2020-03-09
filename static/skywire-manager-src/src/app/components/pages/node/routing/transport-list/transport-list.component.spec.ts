import { async, ComponentFixture, TestBed } from '@angular/core/testing';

import { TransportListComponent } from './transport-list.component';

describe('TransportList', () => {
  let component: TransportListComponent;
  let fixture: ComponentFixture<TransportListComponent>;

  beforeEach(async(() => {
    TestBed.configureTestingModule({
      declarations: [ TransportListComponent ]
    })
    .compileComponents();
  }));

  beforeEach(() => {
    fixture = TestBed.createComponent(TransportListComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});
