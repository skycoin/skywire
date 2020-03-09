import { async, ComponentFixture, TestBed } from '@angular/core/testing';

import { LangButtonComponent } from './lang-button.component';

describe('LangButtonComponent', () => {
  let component: LangButtonComponent;
  let fixture: ComponentFixture<LangButtonComponent>;

  beforeEach(async(() => {
    TestBed.configureTestingModule({
      declarations: [ LangButtonComponent ]
    })
    .compileComponents();
  }));

  beforeEach(() => {
    fixture = TestBed.createComponent(LangButtonComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});
