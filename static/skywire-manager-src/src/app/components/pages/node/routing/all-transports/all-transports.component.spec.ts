import { async, ComponentFixture, TestBed } from '@angular/core/testing';

import { AllTransportsComponent } from './all-transports.component';

describe('AllTransportsComponent', () => {
  let component: AllTransportsComponent;
  let fixture: ComponentFixture<AllTransportsComponent>;

  beforeEach(async(() => {
    TestBed.configureTestingModule({
      declarations: [ AllTransportsComponent ]
    })
    .compileComponents();
  }));

  beforeEach(() => {
    fixture = TestBed.createComponent(AllTransportsComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});
