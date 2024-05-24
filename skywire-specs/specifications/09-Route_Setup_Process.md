# Route Setup Process

1. Route paths are uni-directional. So, the whole route between 2 visors consists of forward and reverse paths. *Setup node* receives both of these paths in the routes setup request. 
2. For each node along both paths *Setup node* calculates how many rules are to be applied.
3. *Setup node* connects to all the node along both paths and sends `ReserveIDs` request to reserve available rule IDs needed to setup the route.
4. *Setup node* creates rules the following way. Let's consider visor A setting up route to visor B. This way we have forward path `A->B` and reverse path `B->A`. For forward path we create `Forward` rule for visor `A`, `IntermediaryForward` rules for each node between `A` and `B`, and `Consume` rule for `B`. For reverse path we create `Forward` rule for visor `B`, `IntermediaryForward` rules for each visor between `B` and `A`, and `Consume` rule for `A`.
5. *Setup node* sends all the created `IntermediaryForward` rules to corresponding visors to be applied.
6. *Setup node* sends `Consume` and `Forward` rules to visor `B` (remote in our case).
7. *Setup node* sends `Forward` and `Consume` rules to visor `A` in response to the route setup request.
