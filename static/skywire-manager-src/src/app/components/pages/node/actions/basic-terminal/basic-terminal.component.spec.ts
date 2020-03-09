import { async, ComponentFixture, TestBed } from '@angular/core/testing';

import { BasicTerminalComponent } from './basic-terminal.component';

describe('BasicTerminalComponent', () => {
  let component: BasicTerminalComponent;
  let fixture: ComponentFixture<BasicTerminalComponent>;

  beforeEach(async(() => {
    TestBed.configureTestingModule({
      declarations: [ BasicTerminalComponent ]
    })
    .compileComponents();
  }));

  beforeEach(() => {
    fixture = TestBed.createComponent(BasicTerminalComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});
