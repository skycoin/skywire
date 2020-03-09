import { async, ComponentFixture, TestBed } from '@angular/core/testing';

import { SelectTabComponent } from './select-tab.component';

describe('SelectTabComponent', () => {
  let component: SelectTabComponent;
  let fixture: ComponentFixture<SelectTabComponent>;

  beforeEach(async(() => {
    TestBed.configureTestingModule({
      declarations: [ SelectTabComponent ],
    })
    .compileComponents();
  }));

  beforeEach(() => {
    fixture = TestBed.createComponent(SelectTabComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should be created', () => {
    expect(component).toBeTruthy();
  });
});
