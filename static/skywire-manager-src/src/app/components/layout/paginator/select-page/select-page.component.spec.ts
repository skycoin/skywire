import { async, ComponentFixture, TestBed } from '@angular/core/testing';

import { SelectPageComponent } from './select-page.component';

describe('SelectPageComponent', () => {
  let component: SelectPageComponent;
  let fixture: ComponentFixture<SelectPageComponent>;

  beforeEach(async(() => {
    TestBed.configureTestingModule({
      declarations: [ SelectPageComponent ],
    })
    .compileComponents();
  }));

  beforeEach(() => {
    fixture = TestBed.createComponent(SelectPageComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should be created', () => {
    expect(component).toBeTruthy();
  });
});
