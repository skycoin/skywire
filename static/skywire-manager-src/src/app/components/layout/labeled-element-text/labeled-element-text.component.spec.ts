import { async, ComponentFixture, TestBed } from '@angular/core/testing';

import { LabeledElementTextComponent } from './labeled-element-text.component';

describe('LabeledElementTextComponent', () => {
  let component: LabeledElementTextComponent;
  let fixture: ComponentFixture<LabeledElementTextComponent>;

  beforeEach(async(() => {
    TestBed.configureTestingModule({
      declarations: [ LabeledElementTextComponent ]
    })
    .compileComponents();
  }));

  beforeEach(() => {
    fixture = TestBed.createComponent(LabeledElementTextComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});
