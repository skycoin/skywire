import { async, ComponentFixture, TestBed } from '@angular/core/testing';

import { AllAppsComponent } from './all-apps.component';

describe('AllAppsComponent', () => {
  let component: AllAppsComponent;
  let fixture: ComponentFixture<AllAppsComponent>;

  beforeEach(async(() => {
    TestBed.configureTestingModule({
      declarations: [ AllAppsComponent ]
    })
    .compileComponents();
  }));

  beforeEach(() => {
    fixture = TestBed.createComponent(AllAppsComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});
