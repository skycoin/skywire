import { async, ComponentFixture, TestBed } from '@angular/core/testing';

import { CreateTransportComponent } from './create-transport.component';

describe('CreateTransportComponent', () => {
  let component: CreateTransportComponent;
  let fixture: ComponentFixture<CreateTransportComponent>;

  beforeEach(async(() => {
    TestBed.configureTestingModule({
      declarations: [ CreateTransportComponent ]
    })
    .compileComponents();
  }));

  beforeEach(() => {
    fixture = TestBed.createComponent(CreateTransportComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});
