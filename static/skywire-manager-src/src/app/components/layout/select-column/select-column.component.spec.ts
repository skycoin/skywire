import { async, ComponentFixture, TestBed } from '@angular/core/testing';

import { SelectColumnComponent } from './select-column.component';

describe('SelectColumnComponent', () => {
  let component: SelectColumnComponent;
  let fixture: ComponentFixture<SelectColumnComponent>;

  beforeEach(async(() => {
    TestBed.configureTestingModule({
      declarations: [ SelectColumnComponent ],
    })
    .compileComponents();
  }));

  beforeEach(() => {
    fixture = TestBed.createComponent(SelectColumnComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should be created', () => {
    expect(component).toBeTruthy();
  });
});
