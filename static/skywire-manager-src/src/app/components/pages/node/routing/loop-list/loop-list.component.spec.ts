import { async, ComponentFixture, TestBed } from '@angular/core/testing';

import { LoopListComponent } from './loop-list.component';

describe('LoopListComponent', () => {
  let component: LoopListComponent;
  let fixture: ComponentFixture<LoopListComponent>;

  beforeEach(async(() => {
    TestBed.configureTestingModule({
      declarations: [ LoopListComponent ]
    })
    .compileComponents();
  }));

  beforeEach(() => {
    fixture = TestBed.createComponent(LoopListComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});
