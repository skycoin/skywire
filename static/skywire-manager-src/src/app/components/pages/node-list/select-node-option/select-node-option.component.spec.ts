import { async, ComponentFixture, TestBed } from '@angular/core/testing';

import { SelectNodeOptionComponent } from './select-node-option.component';

describe('SelectNodeOptionComponent', () => {
  let component: SelectNodeOptionComponent;
  let fixture: ComponentFixture<SelectNodeOptionComponent>;

  beforeEach(async(() => {
    TestBed.configureTestingModule({
      declarations: [ SelectNodeOptionComponent ],
    })
    .compileComponents();
  }));

  beforeEach(() => {
    fixture = TestBed.createComponent(SelectNodeOptionComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should be created', () => {
    expect(component).toBeTruthy();
  });
});
